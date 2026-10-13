package embedded

import (
	"os"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

// warnMissingAppArmorParser warns when the kernel has AppArmor enabled but
// apparmor_parser is not on the PATH. containerd needs the parser to load its
// default profile: without it every container runs unconfined, and a pod that
// asks for an AppArmor profile fails with "apparmor is not supported" because
// the kubelet sees AppArmor enabled. The installers install the parser with
// --install-prereqs; this covers hosts set up some other way.
func warnMissingAppArmorParser(containerMode bool) {
	if containerMode {
		return
	}
	data, err := os.ReadFile("/sys/module/apparmor/parameters/enabled")
	if err != nil || !strings.HasPrefix(string(data), "Y") {
		return
	}
	if _, err := exec.LookPath("apparmor_parser"); err == nil {
		return
	}
	log.Warn().Str("component", "embedded").
		Msg("AppArmor is enabled but apparmor_parser is not on the PATH: containers will run unconfined and pods that request an AppArmor profile will fail to start; install the apparmor package and restart kubesolo")
}
