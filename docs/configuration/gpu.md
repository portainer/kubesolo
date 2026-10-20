# GPUs

KubeSolo can make an NVIDIA or AMD GPU available to pods. It deploys the vendor's device plugin, which advertises the GPU on the node as `nvidia.com/gpu` or `amd.com/gpu`, and any pod can then request one.

It is **off by default**.

---

## Enabling GPU support

```yaml
# /etc/kubesolo/config.yaml
gpu:
  vendor: nvidia        # or amd
```

| Setting | Env var | Default |
|---|---|---|
| `gpu.vendor` | `KUBESOLO_GPU_VENDOR` | `""` |

Changes take effect when KubeSolo restarts. KubeSolo checks the host first (see below); if something is missing, it logs what and starts without GPU support rather than failing. Setting `vendor` back to `""` and restarting deletes the `kubesolo-gpu` namespace and the device plugin in it.

| Vendor | Architectures | Device plugin |
|---|---|---|
| `nvidia` | `amd64`, `arm64` | `nvcr.io/nvidia/k8s-device-plugin:v0.20.1` |
| `amd` | `amd64` | `docker.io/rocm/k8s-device-plugin:1.31.0.11` |

On any other architecture, KubeSolo logs a warning and starts without GPU support.

> **Already followed [containerd drop-ins and NVIDIA GPUs](containerd-drop-ins.md)?** That page installs the device plugin by hand. Uninstall it before setting `gpu.vendor`, or two plugins will advertise the same GPU.

---

## Host prerequisites

KubeSolo does not install drivers. The host provides them.

### NVIDIA

1. The NVIDIA driver. Its version must suit the CUDA version of the images you run.
2. The [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html), for `/usr/bin/nvidia-ctk`.

```bash
nvidia-smi                # lists the GPU and the driver version
ls /usr/bin/nvidia-ctk
```

KubeSolo checks for `/dev/nvidiactl` and `/usr/bin/nvidia-ctk` at startup.

NVIDIA's container runtime is **not** used, and no RuntimeClass is needed. The device plugin generates a [CDI](https://github.com/cncf-tags/container-device-interface) specification in `/var/run/cdi`, and KubeSolo's containerd injects the GPU, the device nodes and the driver libraries into each container that requests one.

### AMD

The `amdgpu` kernel driver, with its firmware, for a GPU that [ROCm supports](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/reference/system-requirements.html). Most distributions ship the driver in the kernel. Nothing else is installed on the host: the ROCm libraries live in each container image.

```bash
ls /dev/kfd /dev/dri
```

KubeSolo checks for `/dev/kfd` at startup.

---

## Using the GPU

Request one GPU in the container's limits:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-check
spec:
  restartPolicy: Never
  containers:
  - name: check
    image: nvidia/cuda:12.8.1-base-ubuntu24.04
    command: ["nvidia-smi"]
    resources:
      limits:
        nvidia.com/gpu: 1
```

```bash
export KUBECONFIG=/var/lib/kubesolo/pki/admin/admin.kubeconfig
kubectl get node -o jsonpath='{.items[0].status.allocatable}'   # includes nvidia.com/gpu
kubectl apply -f gpu-check.yaml
kubectl logs gpu-check                                         # nvidia-smi output
```

For AMD, request `amd.com/gpu: 1` and use an image with ROCm, such as `rocm/rocm-terminal` running `rocm-smi`.

A GPU is allocated whole: while one pod holds it, others that request a GPU stay `Pending`.

---

## Troubleshooting

```bash
journalctl -u kubesolo | grep -i gpu                               # the host check
kubectl -n kubesolo-gpu get pods
kubectl -n kubesolo-gpu logs daemonset/nvidia-device-plugin        # or amdgpu-device-plugin
ls /var/run/cdi                                                    # NVIDIA: the generated CDI spec
```

A pod stuck `Pending` with `Insufficient nvidia.com/gpu` means the device plugin has not advertised the GPU, or another pod holds it.
