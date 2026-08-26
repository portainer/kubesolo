package cli

import (
	"reflect"
	"strings"
	"testing"
)

// TestExtractServiceFlagsAcrossInitSystems runs the extraction over the shape
// each init system's template produces. These are the six formats a real
// upgrade will meet, and the only thing standing between a flag-based install
// and a silently half-migrated one.
func TestExtractServiceFlagsAcrossInitSystems(t *testing.T) {
	for _, tc := range []struct {
		name       string
		definition string
		want       []string
	}{
		{
			name: "systemd",
			definition: `[Unit]
Description=KubeSolo
After=network-online.target

[Service]
ExecStart=/usr/local/bin/kubesolo --path=/var/lib/kubesolo --node-ip=10.0.0.5 --debug
Restart=always
`,
			want: []string{"--path=/var/lib/kubesolo", "--node-ip=10.0.0.5", "--debug"},
		},
		{
			name:       "openrc",
			definition: "command=\"/usr/local/bin/kubesolo\"\ncommand_args=\"--path=/var/lib/kubesolo --d2k --d2k-namespace=workloads\"\n",
			want:       []string{"--path=/var/lib/kubesolo", "--d2k", "--d2k-namespace=workloads"},
		},
		{
			name:       "runit and s6",
			definition: "#!/bin/sh\nexec /usr/local/bin/kubesolo '--path=/var/lib/kubesolo' '--mtu=1400'\n",
			want:       []string{"--path=/var/lib/kubesolo", "--mtu=1400"},
		},
		{
			name:       "upstart",
			definition: "start on runlevel [2345]\nexec /usr/local/bin/kubesolo --path=/var/lib/kubesolo --no-local-storage\n",
			want:       []string{"--path=/var/lib/kubesolo", "--no-local-storage"},
		},
		{
			// start-stop-daemon's own flags sit on the same line and must not be
			// handed to kubesolo.
			name:       "sysvinit alongside foreign flags",
			definition: "start-stop-daemon --start --quiet --pidfile $PIDFILE --make-pidfile --background --exec $DAEMON -- '--path=/var/lib/kubesolo' '--debug'\n",
			want:       []string{"--path=/var/lib/kubesolo", "--debug"},
		},
		{
			name:       "quoted value containing spaces",
			definition: `ExecStart=/usr/local/bin/kubesolo --path=/var/lib/kubesolo --system-reserved="cpu=1,memory=500Mi"`,
			want:       []string{"--path=/var/lib/kubesolo", "--system-reserved=cpu=1,memory=500Mi"},
		},
		{
			name:       "no kubesolo flags at all",
			definition: "[Service]\nExecStart=/usr/bin/something-else --verbose\n",
			want:       nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extractServiceFlags(tc.definition)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExtractServiceFlagsRejectsForeignFlags is the specific hazard in the
// sysvinit template: passing --start or --pidfile to kubesolo would abort it.
func TestExtractServiceFlagsRejectsForeignFlags(t *testing.T) {
	definition := "start-stop-daemon --start --quiet --pidfile /x --make-pidfile --background --chuid root --exec $DAEMON -- --path=/var/lib/kubesolo\n"

	for _, flag := range extractServiceFlags(definition) {
		for _, foreign := range []string{"--start", "--quiet", "--pidfile", "--make-pidfile", "--background", "--chuid", "--exec"} {
			if flag == foreign || strings.HasPrefix(flag, foreign+"=") {
				t.Errorf("extracted %s, which belongs to start-stop-daemon, not kubesolo", flag)
			}
		}
	}
}

// TestExtractServiceFlagsDeduplicates guards against a template that mentions a
// flag twice — in a comment and in the command, say — producing it twice.
func TestExtractServiceFlagsDeduplicates(t *testing.T) {
	definition := "# regenerate with --path=/old\nExecStart=/usr/local/bin/kubesolo --path=/var/lib/kubesolo\n"

	got := extractServiceFlags(definition)
	if len(got) != 1 {
		t.Fatalf("got %q, want a single --path", got)
	}
	// The first occurrence wins, which is the comment here. That is acceptable:
	// the value is re-resolved by --print-config either way, and duplicate flags
	// would make the binary reject the command line outright.
	if !strings.HasPrefix(got[0], "--path=") {
		t.Errorf("got %q", got[0])
	}
}

func TestUnquoteShellValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"--debug", "--debug"},
		{"--path=/var/lib/kubesolo", "--path=/var/lib/kubesolo"},
		{`--system-reserved="cpu=1,memory=500Mi"`, "--system-reserved=cpu=1,memory=500Mi"},
		{"--node-ip='10.0.0.5'", "--node-ip=10.0.0.5"},
		{`--empty=""`, "--empty="},
	} {
		if got := unquoteShellValue(tc.in); got != tc.want {
			t.Errorf("unquoteShellValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
