package embedded

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

// SELinuxPolicyModule is the name of the SELinux policy module kubesolo installs.
const SELinuxPolicyModule = "kubesolo"

// selinuxPolicy lets systemd attach the BPF device filter the embedded crun
// builds for every container. crun runs in kubesolo's domain,
// unconfined_service_t, and hands the program to systemd (init_t) to attach to
// the container's scope. The targeted policy denies init_t prog_run on
// unconfined_service_t, and a dontaudit rule keeps the denial out of the audit
// log, so under enforcing mode every pod sandbox fails with "systemd failed to
// install eBPF device filter on cgroup" and nothing says why.
const selinuxPolicy = "(allow init_t unconfined_service_t (bpf (prog_run)))\n"

// ensureSELinuxPolicy installs the kubesolo SELinux policy module when SELinux
// is enabled, in permissive mode too so that switching to enforcing later does
// not break pods. A failure is logged rather than returned: on a policy without
// these types the module cannot load, and kubesolo may still work there.
func ensureSELinuxPolicy(containerMode bool) {
	if containerMode || !selinuxEnabled() {
		return
	}

	semodule, err := exec.LookPath("semodule")
	if err != nil {
		log.Warn().Str("component", "embedded").
			Msg("SELinux is enabled but semodule is not on the PATH, so the kubesolo policy module cannot be installed; under enforcing mode pods will fail to start (install policycoreutils)")
		return
	}

	if out, err := exec.Command(semodule, "-l").Output(); err == nil && moduleListed(out, SELinuxPolicyModule) {
		log.Debug().Str("component", "embedded").Msg("SELinux policy module kubesolo already installed")
		return
	}

	// semodule names a CIL module after its file, so the file must be kubesolo.cil.
	dir, err := os.MkdirTemp("", "kubesolo-selinux-")
	if err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to stage the SELinux policy module: %v", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, SELinuxPolicyModule+".cil")
	if err := os.WriteFile(path, []byte(selinuxPolicy), 0o600); err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to stage the SELinux policy module: %v", err)
		return
	}

	log.Info().Str("component", "embedded").Msg("installing SELinux policy module kubesolo, so systemd can attach container device filters...")
	if out, err := exec.Command(semodule, "-i", path).CombinedOutput(); err != nil {
		log.Warn().Str("component", "embedded").
			Msgf("failed to install the SELinux policy module kubesolo: %v (output: %s); under enforcing mode pods will fail to start", err, bytes.TrimSpace(out))
		return
	}
	log.Info().Str("component", "embedded").Msg("SELinux policy module kubesolo installed")
}

// selinuxEnabled reports whether SELinux is enabled, in enforcing or
// permissive mode. selinuxfs exposes the enforce file only then.
func selinuxEnabled() bool {
	_, err := os.Stat("/sys/fs/selinux/enforce")
	return err == nil
}

// moduleListed reports whether semodule -l output lists name. Older semodule
// releases print a version after the name, newer ones print the name alone.
func moduleListed(out []byte, name string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := bytes.Fields(scanner.Bytes())
		if len(fields) > 0 && string(fields[0]) == name {
			return true
		}
	}
	return false
}
