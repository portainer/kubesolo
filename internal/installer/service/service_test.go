package service

import (
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/portainer/kubesolo/internal/installer/detect"
)

// ── renderTemplate ────────────────────────────────────────────────────────────

func TestRenderTemplate_SystemdUnit_NoProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		Proxy:       "",
	}
	out, err := renderTemplate("systemd", systemdUnitTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "[Unit]")
	assertContains(t, out, "[Service]")
	assertContains(t, out, "[Install]")
	assertContains(t, out, "ExecStart=/usr/local/bin/kubesolo --path=/var/lib/kubesolo")
	assertContains(t, out, "WantedBy=multi-user.target")
	assertNotContains(t, out, "HTTP_PROXY")
}

func TestRenderTemplate_SystemdUnit_WithProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		Proxy:       "http://proxy.corp.com:8080",
	}
	out, err := renderTemplate("systemd", systemdUnitTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, `Environment="HTTP_PROXY=http://proxy.corp.com:8080"`)
	assertContains(t, out, `Environment="HTTPS_PROXY=http://proxy.corp.com:8080"`)
	assertContains(t, out, `Environment="NO_PROXY=localhost,127.0.0.1"`)
}

func TestRenderTemplate_OpenRC_NoProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
	}
	out, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "#!/sbin/openrc-run")
	assertContains(t, out, `command="/usr/local/bin/kubesolo"`)
	assertContains(t, out, "need net")
}

func TestRenderTemplate_OpenRC_WithProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		Proxy:       "http://proxy.example.com:3128",
	}
	out, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, `export HTTP_PROXY="http://proxy.example.com:3128"`)
	assertContains(t, out, `export HTTPS_PROXY="http://proxy.example.com:3128"`)
}

func TestRenderTemplate_SysVInit(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo --debug=true",
	}
	out, err := renderTemplate("sysvinit", sysvinitTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "### BEGIN INIT INFO")
	assertContains(t, out, "### END INIT INFO")
	assertContains(t, out, "start-stop-daemon")
	assertContains(t, out, "start|stop|restart|status")
}

func TestRenderTemplate_S6Run(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
	}
	out, err := renderTemplate("s6-run", s6RunTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "exec /usr/local/bin/kubesolo")
}

func TestRenderTemplate_Upstart(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
	}
	out, err := renderTemplate("upstart", upstartTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "respawn")
	assertContains(t, out, "exec /usr/local/bin/kubesolo")
	assertContains(t, out, "start on runlevel [2345]")
}

func TestRenderTemplate_Runit(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
	}
	out, err := renderTemplate("runit-run", runitRunTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "exec /usr/local/bin/kubesolo")
}

// ── buildTemplateData ─────────────────────────────────────────────────────────

func TestBuildTemplateData(t *testing.T) {
	cfg := &config.Config{
		Path:  "/var/lib/kubesolo",
		Proxy: "http://proxy:8080",
	}
	cmdArgs := []string{"--path=/var/lib/kubesolo", "--debug=true"}
	data := buildTemplateData(cfg, cmdArgs)

	if data.AppName != config.AppName {
		t.Errorf("AppName = %q, want %q", data.AppName, config.AppName)
	}
	if data.InstallPath != config.DefaultInstallPath {
		t.Errorf("InstallPath = %q, want %q", data.InstallPath, config.DefaultInstallPath)
	}
	if data.CmdArgs != "--path=/var/lib/kubesolo --debug=true" {
		t.Errorf("CmdArgs = %q, want joined args", data.CmdArgs)
	}
	if data.Proxy != "http://proxy:8080" {
		t.Errorf("Proxy = %q, want %q", data.Proxy, "http://proxy:8080")
	}
}

// ── New / manager routing ─────────────────────────────────────────────────────

func TestNew_RunModeDaemon(t *testing.T) {
	info := &detect.SystemInfo{InitSystem: detect.InitSystemd}
	mgr, err := New(info, config.RunModeDaemon)
	if err != nil {
		t.Fatalf("New(daemon) error: %v", err)
	}
	if _, ok := mgr.(*daemonManager); !ok {
		t.Errorf("New(daemon) should return *daemonManager, got %T", mgr)
	}
}

func TestNew_RunModeForeground(t *testing.T) {
	info := &detect.SystemInfo{InitSystem: detect.InitSystemd}
	mgr, err := New(info, config.RunModeForeground)
	if err != nil {
		t.Fatalf("New(foreground) error: %v", err)
	}
	if _, ok := mgr.(*foregroundManager); !ok {
		t.Errorf("New(foreground) should return *foregroundManager, got %T", mgr)
	}
}

func TestNew_InitSystemRouting(t *testing.T) {
	cases := []struct {
		init    detect.InitSystem
		wantTyp string
	}{
		{detect.InitSystemd, "*service.systemdManager"},
		{detect.InitOpenRC, "*service.openrcManager"},
		{detect.InitSysV, "*service.sysvinitManager"},
		{detect.InitS6, "*service.s6Manager"},
		{detect.InitRunit, "*service.runitManager"},
		{detect.InitUpstart, "*service.upstartManager"},
		{detect.InitUnknown, "*service.daemonManager"}, // fallback
	}
	for _, c := range cases {
		info := &detect.SystemInfo{InitSystem: c.init}
		mgr, err := New(info, config.RunModeService)
		if err != nil {
			t.Errorf("New(service, %q) unexpected error: %v", c.init, err)
			continue
		}
		// Type name comparison (works without reflect)
		typeName := typeName(mgr)
		if !strings.HasSuffix(typeName, strings.TrimPrefix(c.wantTyp, "*service.")) {
			t.Errorf("New(service, %q) = %T, want type containing %q", c.init, mgr, c.wantTyp)
		}
	}
}

func TestNew_InvalidRunMode(t *testing.T) {
	info := &detect.SystemInfo{InitSystem: detect.InitSystemd}
	_, err := New(info, "bananas")
	if err == nil {
		t.Fatal("expected error for invalid run mode, got nil")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected output to contain %q\nfull output:\n%s", sub, s)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("expected output NOT to contain %q\nfull output:\n%s", sub, s)
	}
}

// typeName returns a short string identifying the concrete type of v.
func typeName(v interface{}) string {
	switch v.(type) {
	case *systemdManager:
		return "systemdManager"
	case *openrcManager:
		return "openrcManager"
	case *sysvinitManager:
		return "sysvinitManager"
	case *s6Manager:
		return "s6Manager"
	case *runitManager:
		return "runitManager"
	case *upstartManager:
		return "upstartManager"
	case *daemonManager:
		return "daemonManager"
	case *foregroundManager:
		return "foregroundManager"
	default:
		return "unknown"
	}
}
