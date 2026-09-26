package bootstrap

import (
	"fmt"

	"k8s.io/client-go/tools/clientcmd"
)

// TokenFromKubeconfig reads the bootstrap token out of a kubeconfig on disk.
//
// A host that manages its own kubelet generally mints the token itself and
// hands it to the kubelet this way — Talos writes it to
// /etc/kubernetes/bootstrap-kubeconfig — so this is how KubeSolo learns a token
// it could not have been configured with. The file is read at startup rather
// than watched: the token is fixed for the life of the cluster.
func TokenFromKubeconfig(path string) (string, error) {
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read bootstrap kubeconfig %s: %v", path, err)
	}

	// Take the token from whichever user the current context selects, so a
	// kubeconfig carrying more than one credential cannot pick the wrong one.
	context, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return "", fmt.Errorf("bootstrap kubeconfig %s has no context named %q", path, config.CurrentContext)
	}

	authInfo, ok := config.AuthInfos[context.AuthInfo]
	if !ok {
		return "", fmt.Errorf("bootstrap kubeconfig %s has no user named %q", path, context.AuthInfo)
	}

	// A kubeconfig holding a client certificate is a kubelet that has already
	// enrolled, not one waiting to. Bootstrapping it again would be a no-op at
	// best, so say what was found instead of failing later at the API server.
	if authInfo.Token == "" {
		return "", fmt.Errorf("bootstrap kubeconfig %s carries no token for user %q; it is not a bootstrap kubeconfig", path, context.AuthInfo)
	}

	return authInfo.Token, nil
}
