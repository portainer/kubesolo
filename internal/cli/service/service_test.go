package service

import (
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
)

// ── renderTemplate ────────────────────────────────────────────────────────────

func TestRenderTemplate_SystemdUnit_NoProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
		Proxy:       "",
	}
	out, err := renderTemplate("systemd", systemdUnitTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "[Unit]")
	assertContains(t, out, "[Service]")
	assertContains(t, out, "[Install]")
	assertContains(t, out, "ExecStart=/usr/local/bin/kubesolo '--path=/var/lib/kubesolo'")
	assertContains(t, out, "WantedBy=multi-user.target")
	assertNotContains(t, out, "HTTP_PROXY")
}

func TestRenderTemplate_SystemdUnit_WithProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
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

// TestRenderTemplate_Systemd_ProxyWithSpecialChars verifies that a proxy URL
// containing characters that are special in systemd unit files cannot break
// out of the Environment= double-quoted value.
func TestRenderTemplate_Systemd_ProxyWithSpecialChars(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgsList: []string{},
		// " breaks naive double-quote wrapping; % triggers systemd specifier expansion
		Proxy: `http://user:"p@ss%word"@proxy:3128`,
	}
	out, err := renderTemplate("systemd", systemdUnitTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	// The output must contain the correctly escaped form:
	// " → \", % → %%
	assertContains(t, out, `Environment="HTTP_PROXY=http://user:\"p@ss%%word\"@proxy:3128"`)
	// There must be no raw unescaped " inside the Environment= value
	assertNotContains(t, out, `HTTP_PROXY=http://user:"p`)
}

func TestRenderTemplate_OpenRC_NoProxy(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
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
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
		Proxy:       "http://proxy.example.com:3128",
	}
	out, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	// Proxy is now single-quoted, not double-quoted
	assertContains(t, out, `export HTTP_PROXY='http://proxy.example.com:3128'`)
	assertContains(t, out, `export HTTPS_PROXY='http://proxy.example.com:3128'`)
}

// TestRenderTemplate_OpenRC_ProxyWithQuote verifies that a proxy URL containing
// a double-quote is safely wrapped in single quotes rather than double-quoted,
// so the " character cannot break out of the shell assignment.
func TestRenderTemplate_OpenRC_ProxyWithQuote(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgsList: []string{},
		Proxy:       `http://user:"secret"@proxy:3128`,
	}
	out, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	// The value must be single-quoted, keeping " safely inside the literal string.
	assertContains(t, out, `export HTTP_PROXY='http://user:"secret"@proxy:3128'`)
	// There must be no unprotected double-quoted export line
	assertNotContains(t, out, `export HTTP_PROXY="`)
}

// TestRenderTemplate_OpenRC_ProxyWithSingleQuote verifies that a proxy URL
// containing a single-quote is escaped using the '"'"' POSIX idiom.
func TestRenderTemplate_OpenRC_ProxyWithSingleQuote(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgsList: []string{},
		Proxy:       `http://user:it's@proxy:3128`,
	}
	out, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	// The single-quote in the password must be escaped with the '"'"' idiom
	assertContains(t, out, `'http://user:it'"'"'s@proxy:3128'`)
}

func TestRenderTemplate_SysVInit(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo --debug",
		CmdArgsList: []string{"--path=/var/lib/kubesolo", "--debug"},
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
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
	}
	out, err := renderTemplate("s6-run", s6RunTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "exec /usr/local/bin/kubesolo '--path=/var/lib/kubesolo'")
}

func TestRenderTemplate_Upstart(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
	}
	out, err := renderTemplate("upstart", upstartTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "respawn")
	assertContains(t, out, "exec /usr/local/bin/kubesolo '--path=/var/lib/kubesolo'")
	assertContains(t, out, "start on runlevel [2345]")
}

func TestRenderTemplate_Runit(t *testing.T) {
	data := templateData{
		AppName:     "kubesolo",
		InstallPath: "/usr/local/bin/kubesolo",
		CmdArgs:     "--path=/var/lib/kubesolo",
		CmdArgsList: []string{"--path=/var/lib/kubesolo"},
	}
	out, err := renderTemplate("runit-run", runitRunTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate failed: %v", err)
	}
	assertContains(t, out, "exec /usr/local/bin/kubesolo '--path=/var/lib/kubesolo'")
}

// ── buildTemplateData ─────────────────────────────────────────────────────────

func TestBuildTemplateData(t *testing.T) {
	cfg := &config.Config{
		Path:  "/var/lib/kubesolo",
		Proxy: "http://proxy:8080",
	}
	cmdArgs := []string{"--path=/var/lib/kubesolo", "--debug"}
	data := buildTemplateData(cfg, cmdArgs)

	if data.AppName != config.AppName {
		t.Errorf("AppName = %q, want %q", data.AppName, config.AppName)
	}
	if data.InstallPath != config.DefaultInstallPath {
		t.Errorf("InstallPath = %q, want %q", data.InstallPath, config.DefaultInstallPath)
	}
	if data.CmdArgs != "--path=/var/lib/kubesolo --debug" {
		t.Errorf("CmdArgs = %q, want joined args", data.CmdArgs)
	}
	if len(data.CmdArgsList) != 2 || data.CmdArgsList[0] != "--path=/var/lib/kubesolo" || data.CmdArgsList[1] != "--debug" {
		t.Errorf("CmdArgsList = %v, want original args", data.CmdArgsList)
	}
	if data.Proxy != "http://proxy:8080" {
		t.Errorf("Proxy = %q, want %q", data.Proxy, "http://proxy:8080")
	}
}

func TestBuildTemplateData_NewlineSanitization(t *testing.T) {
	cfg := &config.Config{
		Proxy: "http://proxy:8080\nmalicious: injected",
	}
	cmdArgs := []string{"--path=/var/lib/kubesolo\ninjected=bad"}
	data := buildTemplateData(cfg, cmdArgs)

	if strings.Contains(data.Proxy, "\n") {
		t.Errorf("Proxy should have newlines stripped, got: %q", data.Proxy)
	}
	if strings.Contains(data.CmdArgs, "\n") {
		t.Errorf("CmdArgs should have newlines stripped, got: %q", data.CmdArgs)
	}
	for _, a := range data.CmdArgsList {
		if strings.Contains(a, "\n") {
			t.Errorf("CmdArgsList entry should have newlines stripped, got: %q", a)
		}
	}
}

// ── escaping helper unit tests ────────────────────────────────────────────────

func TestShellQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's alive", `'it'"'"'s alive'`},
		{"http://proxy:8080", "'http://proxy:8080'"},
		// newlines must be stripped, not passed through
		{"foo\nbar", "'foobar'"},
		{"foo\r\nbar", "'foobar'"},
	}
	for _, c := range cases {
		got := shellQuote(c.input)
		if got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestShellDoubleQuoteVal(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`say "hi"`, `say \"hi\"`},
		{`back\slash`, `back\\slash`},
		{"tick`cmd`", "tick\\`cmd\\`"},
		{"$PATH", `\$PATH`},
		{"foo\nbar", "foobar"},
	}
	for _, c := range cases {
		got := shellDoubleQuoteVal(c.input)
		if got != c.want {
			t.Errorf("shellDoubleQuoteVal(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestSystemdEnvVal(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"http://proxy:8080", "http://proxy:8080"},
		{`has"quote`, `has\"quote`},
		{`back\slash`, `back\\slash`},
		{"percent%unit", "percent%%unit"},
		{"foo\nbar", "foobar"},
	}
	for _, c := range cases {
		got := systemdEnvVal(c.input)
		if got != c.want {
			t.Errorf("systemdEnvVal(%q) = %q, want %q", c.input, got, c.want)
		}
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
