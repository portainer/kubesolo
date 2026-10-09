package upgrade

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/types"
)

// The boot guard is what makes a bad upgrade cost nothing when the executor is
// not there to see it fail — the host lost power, or rebooted, while a new
// binary was still unproven. It cannot live in the kubesolo binary: the binary
// it protects against may not get as far as running any of its own code. So it
// is a POSIX shell script, and every init system runs it before KubeSolo.
//
// While PendingFile exists, each start increments AttemptsFile. Once
// MaxBootAttempts starts have failed to clear it, the guard puts the previous
// binary, datastore and configuration file back before letting the start go
// ahead. A healthy KubeSolo removes PendingFile, after which the guard does
// nothing.

// guardMarker identifies the hook in a service definition, so installing it is
// idempotent.
const guardMarker = "kubesolo-upgrade-guard"

var guardTemplate = template.Must(template.New("guard").Parse(`#!/bin/sh
# {{.Marker}}: written by the KubeSolo upgrade, run by the init system before
# every start of KubeSolo. Does nothing unless an upgrade is being verified.
dir='{{.Dir}}'
[ -f "$dir/pending" ] || exit 0

attempts=$(cat "$dir/attempts" 2>/dev/null)
case "$attempts" in ''|*[!0-9]*) attempts=0 ;; esac
if [ "$attempts" -lt {{.Max}} ]; then
    echo $((attempts + 1)) > "$dir/attempts"
    exit 0
fi

pending=$(cat "$dir/pending")
backup="$dir/backup"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [ ! -f "$backup/kubesolo" ] || [ ! -f "$backup/{{.DBFile}}" ]; then
    echo "$now $pending failed $attempts starts, but the backup is incomplete; nothing restored" >> "$dir/guard.log"
    rm -f "$dir/pending" "$dir/attempts"
    exit 0
fi

# Stop what the failed version left running, so the restored one does not
# start a second copy beside it: pod processes, then this containerd's shims.
for procs in $(find '{{.CgroupRoot}}' -path '*kubepods*' -name cgroup.procs 2>/dev/null); do
    for pid in $(cat "$procs" 2>/dev/null); do kill -9 "$pid" 2>/dev/null; done
done
for p in '{{.ProcRoot}}'/[0-9]*; do
    case "$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null)" in
        *containerd-shim*'-address {{.Socket}}'*) kill -9 "${p#/proc/}" 2>/dev/null ;;
    esac
done

cp "$backup/kubesolo" '{{.Binary}}.guard' && chmod 755 '{{.Binary}}.guard' && mv -f '{{.Binary}}.guard' '{{.Binary}}' || {
    echo "$now $pending failed $attempts starts; restoring the binary failed" >> "$dir/guard.log"
    exit 0
}
rm -f '{{.DB}}-wal' '{{.DB}}-shm'
cp "$backup/{{.DBFile}}" '{{.DB}}.guard' && mv -f '{{.DB}}.guard' '{{.DB}}'
if [ -f "$backup/config.yaml" ]; then
    cp "$backup/config.yaml" '{{.ConfigFile}}.guard' && mv -f '{{.ConfigFile}}.guard' '{{.ConfigFile}}'
fi
from=$(sed -n 's/.*"from": *"\([^"]*\)".*/\1/p' "$backup/manifest.json" 2>/dev/null)
echo "$now $pending failed $attempts starts; restored ${from:-the previous version} and its datastore" >> "$dir/guard.log"
rm -f "$dir/pending" "$dir/attempts"
exit 0
`))

// RenderGuard returns the boot guard script for a layout.
func RenderGuard(l Layout) ([]byte, error) {
	return renderGuard(l, "/sys/fs/cgroup", "/proc")
}

// renderGuard takes the cgroup and proc roots so that tests can run the script
// against fakes of them, rather than against the host's workloads.
func renderGuard(l Layout, cgroupRoot, procRoot string) ([]byte, error) {
	var buf bytes.Buffer
	err := guardTemplate.Execute(&buf, map[string]any{
		"Marker":     guardMarker,
		"Dir":        l.Dir(),
		"Max":        MaxBootAttempts,
		"DBFile":     types.DefaultKineDBFile,
		"DB":         l.Datastore(),
		"Binary":     l.Binary,
		"ConfigFile": l.ConfigFile,
		"Socket":     filepath.Join(l.DataDir, types.DefaultContainerdDir, types.DefaultContainerdSocket),
		"CgroupRoot": cgroupRoot,
		"ProcRoot":   procRoot,
	})
	return buf.Bytes(), err
}

// guardLine is the shell line a service definition runs the guard with. It
// never fails: a missing or broken guard must not stop KubeSolo starting.
func guardLine(l Layout) string {
	return fmt.Sprintf("[ -f '%s' ] && /bin/sh '%s' || true # %s", l.GuardScript(), l.GuardScript(), guardMarker)
}

// systemdDropIn is where the guard hook goes for systemd. A drop-in leaves the
// unit itself alone, so it works the same for units written by kubesoloctl and
// by install.sh, and survives either rewriting the unit.
const systemdDropIn = "/etc/systemd/system/kubesolo.service.d/10-upgrade-guard.conf"

// InstallGuard writes the guard script and makes sure the service runs it
// before KubeSolo starts. It is idempotent. Daemon mode has no init system to
// restart a failed KubeSolo, so there is nothing for a guard to count, and the
// executor alone watches the new version.
func InstallGuard(l Layout, svc *Service) error {
	script, err := RenderGuard(l)
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(l.GuardScript(), script, 0o700); err != nil {
		return fmt.Errorf("write the boot guard: %w", err)
	}
	if svc.Daemon {
		return nil
	}

	switch svc.Init {
	case detect.InitSystemd:
		// The leading "-" makes a missing or failing guard harmless.
		//
		// KillMode=process is here too, for units install.sh wrote before it
		// set it. With systemd's default, control-group, the stop below kills
		// the containerd shims but not the containers, which carry on running
		// where the new version cannot reattach to them, and it starts a second
		// copy of every pod. A drop-in changes a running unit's KillMode once
		// systemd has reloaded, so it is in force for this upgrade's own stop.
		want := fmt.Sprintf("# %s\n[Service]\nExecStartPre=-/bin/sh %s\nKillMode=process\nDelegate=yes\n", guardMarker, l.GuardScript())
		if cur, err := os.ReadFile(systemdDropIn); err == nil && string(cur) == want {
			return nil
		}
		if err := WriteFileAtomic(systemdDropIn, []byte(want), 0o644); err != nil {
			return err
		}
		if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case detect.InitOpenRC, detect.InitSysV:
		return patchDefinition(initDScript, func(def string) (string, error) {
			if strings.HasPrefix(def, "#!/sbin/openrc-run") {
				return PatchOpenRC(def, l)
			}
			return PatchSysV(def, l)
		})
	case detect.InitS6:
		return patchDefinition(filepath.Join(s6Dir, "run"), func(def string) (string, error) { return PatchRunScript(def, l) })
	case detect.InitRunit:
		return patchDefinition(filepath.Join(runitDir, "run"), func(def string) (string, error) { return PatchRunScript(def, l) })
	case detect.InitUpstart:
		return patchDefinition(upstartJob, func(def string) (string, error) { return PatchUpstart(def, l) })
	}
	return fmt.Errorf("cannot install the boot guard for init system %q", svc.Init)
}

// killModeDropIn makes systemd stop only KubeSolo's own process; see
// EnsureProcessKillMode.
const killModeDropIn = "/etc/systemd/system/kubesolo.service.d/05-killmode.conf"

// EnsureProcessKillMode makes a systemd kubesolo unit stop with
// KillMode=process before anything stops it, and reports whether it changed
// anything. Units install.sh wrote before it set KillMode use systemd's
// default, control-group, which kills the containerd shims on stop but not the
// containers: they carry on running where no KubeSolo can reattach to them,
// and the next one starts a second copy of every pod. A drop-in applies to the
// running unit once systemd has reloaded.
func EnsureProcessKillMode() (bool, error) {
	if _, err := os.Stat(systemdUnit); err != nil {
		return false, nil
	}
	if out, err := exec.Command("systemctl", "show", "kubesolo", "-p", "KillMode", "--value").Output(); err == nil && strings.TrimSpace(string(out)) == "process" {
		return false, nil
	}
	if err := WriteFileAtomic(killModeDropIn, []byte("# "+guardMarker+"\n[Service]\nKillMode=process\nDelegate=yes\n"), 0o644); err != nil {
		return false, err
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return false, fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// RemoveGuardHook removes the systemd drop-in. The hooks patched into other
// service definitions go with those definitions on uninstall.
func RemoveGuardHook() {
	_ = os.Remove(systemdDropIn)
	_ = os.Remove(killModeDropIn)
	_ = os.Remove(filepath.Dir(systemdDropIn))
}

func patchDefinition(path string, patch func(string) (string, error)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	def := string(raw)
	if strings.Contains(def, guardMarker) {
		return nil
	}
	out, err := patch(def)
	if err != nil {
		return fmt.Errorf("install the boot guard into %s: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, []byte(out), fi.Mode().Perm())
}

// PatchOpenRC adds a start_pre to an OpenRC service script. openrc-run sources
// the whole script, so a function appended at the end is defined like any
// other. A script that already has its own start_pre is not one KubeSolo
// wrote, and is left for a human.
//
// Scripts written before KubeSolo sent its output to syslog also get the
// loggers: without them OpenRC discards everything KubeSolo logs. Each stream
// is checked on its own, and one the script already sends somewhere is left
// alone.
func PatchOpenRC(def string, l Layout) (string, error) {
	if regexp.MustCompile(`(?m)^\s*start_pre\s*\(\)`).MatchString(def) {
		return "", fmt.Errorf("the script already defines start_pre")
	}
	out := strings.TrimRight(def, "\n") + "\n"
	var loggers string
	for _, stream := range []string{"output", "error"} {
		if !regexp.MustCompile(`(?m)^\s*` + stream + `_log(ger)?=`).MatchString(def) {
			loggers += stream + "_logger=\"logger -t kubesolo -p daemon.info\"\n"
		}
	}
	if loggers != "" {
		out += "\n" + loggers
	}
	return out + "\nstart_pre() {\n    " + guardLine(l) + "\n}\n", nil
}

// PatchSysV runs the guard at the start of the start) branch.
func PatchSysV(def string, l Layout) (string, error) {
	re := regexp.MustCompile(`(?m)^(\s*)start\)\s*$`)
	loc := re.FindStringSubmatchIndex(def)
	if loc == nil {
		return "", fmt.Errorf("no start) branch found")
	}
	indent := def[loc[2]:loc[3]]
	return def[:loc[1]] + "\n" + indent + "    " + guardLine(l) + def[loc[1]:], nil
}

// PatchRunScript runs the guard before the exec line of an s6 or runit run
// script.
func PatchRunScript(def string, l Layout) (string, error) {
	re := regexp.MustCompile(`(?m)^exec `)
	loc := re.FindStringIndex(def)
	if loc == nil {
		return "", fmt.Errorf("no exec line found")
	}
	return def[:loc[0]] + guardLine(l) + "\n" + def[loc[0]:], nil
}

// PatchUpstart adds a pre-start script to an Upstart job.
func PatchUpstart(def string, l Layout) (string, error) {
	if regexp.MustCompile(`(?m)^\s*pre-start\b`).MatchString(def) {
		return "", fmt.Errorf("the job already has a pre-start stanza")
	}
	re := regexp.MustCompile(`(?m)^exec `)
	loc := re.FindStringIndex(def)
	if loc == nil {
		return "", fmt.Errorf("no exec stanza found")
	}
	stanza := "pre-start script\n    " + guardLine(l) + "\nend script\n\n"
	return def[:loc[0]] + stanza + def[loc[0]:], nil
}
