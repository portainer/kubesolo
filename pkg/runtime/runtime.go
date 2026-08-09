// Package runtime selects the container runtime the rest of kubesolo depends on:
// the containerd kubesolo embeds and supervises, or a runtime the host manages that
// kubesolo merely attaches to. Which one is used is decided by
// --container-runtime-endpoint, and by the external_deps build tag — builds carrying
// it do not link containerd at all.
package runtime

// Service is a container runtime kubesolo depends on. Run blocks until the runtime
// stops or the context is cancelled, and signals readiness on the channel it was
// constructed with.
type Service interface {
	Run() error
}
