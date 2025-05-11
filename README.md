# kubesolo

Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments such as IoT or IIoT devices running in embedded environments.

## What is this?

KubeSolo is a production-ready single-node Kubernetes distribution with the following changes:

1. It is packaged as a single binary
2. It uses SQLite (via Kine) as the default storage backend
3. It wraps Kubernetes and other components in a single, simple launcher
4. It is secure by default with reasonable defaults for lightweight environments
5. It has minimal OS dependencies (just a sane kernel and cgroup mounts needed)
6. It eliminates the need for complex multi-node setup by providing a single-node solution

KubeSolo bundles the following technologies together into a single cohesive distribution:

* Containerd & runc for container runtime
* CoreDNS for DNS resolution
* Kine for SQLite-based storage

## What's with the name?

KubeSolo is designed to be a single-node Kubernetes distribution, hence the "Solo" in the name. It's meant to be simple, lightweight, and perfect for development, testing, or small production workloads that don't require the complexity of a multi-node cluster.

## Is this a fork?

No, it's a distribution. A fork implies continued divergence from the original. This is not KubeSolo's goal or practice. KubeSolo explicitly intends not to change any core Kubernetes functionality. We seek to remain as close to upstream Kubernetes as possible by leveraging the k3s forked Kubernetes. However, we maintain a small set of patches important to KubeSolo's use case and deployment model.

## How is this lightweight or smaller than upstream Kubernetes?

There are three major ways that KubeSolo is lighter weight than upstream Kubernetes:

1. The memory footprint to run it is smaller
2. The binary, which contains all the non-containerized components needed to run a cluster, is smaller
3. The Kubernetes Scheduler does not exist. This is replaced by a custom Webhook called `NodeSetter`

The memory footprint is reduced primarily by:
* Running many components inside of a single process
* Using SQLite instead of etcd
* Optimizing resource limits for single-node usage

## Getting Started

### Quick Install

```bash
# Download and install KubeSolo
curl -sfL https://get.kubesolo.io | sh -
```

A kubeconfig file is written to `/var/lib/kubesolo/pki/admin/admin.kubeconfig` and the service is automatically started.

## Documentation

Please see the [documentation](docs/) for complete documentation.

## Community

* ### Getting involved

GitHub Issues - Submit your issues and feature requests via GitHub.

* ### Community Meetings

Join our community meetings to chat with KubeSolo developers and other users.

## Release cadence

KubeSolo maintains pace with upstream Kubernetes releases. Our goal is to release patch releases within one week, and new minors within 30 days.

Our release versioning reflects the version of upstream Kubernetes that is being released. For example, the KubeSolo release v1.27.4+kubesolo1 maps to the `v1.27.4` Kubernetes release.

## Contributing

Please check out our [contributing guide](CONTRIBUTING.md) if you're interested in contributing to KubeSolo.

## Security

Security issues in KubeSolo can be reported by sending an email to security@kubesolo.io. Please do not file issues about security issues.

## License

MIT License