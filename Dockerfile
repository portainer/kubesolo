# kubesolo Container Image
#
# Build:
#   make image
#
# Run:
#   docker run -d --privileged \
#     --hostname kubesolo \
#     --security-opt seccomp=unconfined \
#     --security-opt apparmor=unconfined \
#     --tmpfs /tmp --tmpfs /run \
#     -v /lib/modules:/lib/modules:ro \
#     -v kubesolo-data:/var/lib/kubesolo \
#     -p 6443:6443 \
#     --name kubesolo \
#     portainer/kubesolo:latest
#
# Get kubeconfig:
#   docker exec kubesolo cat /var/lib/kubesolo/pki/admin/admin.kubeconfig > kubeconfig.json
#   sed -i 's|https://[^"]*:6443|https://127.0.0.1:6443|' kubeconfig.json
#   export KUBECONFIG=$(pwd)/kubeconfig.json
#
# Stop:
#   docker stop kubesolo && docker rm kubesolo

FROM alpine:3.21

RUN apk add --no-cache \
    iptables \
    ip6tables \
    conntrack-tools \
    iproute2 \
    kmod \
    e2fsprogs \
    ca-certificates \
    && mkdir -p /var/lib/kubesolo

COPY dist/kubesolo /usr/local/bin/kubesolo
RUN chmod +x /usr/local/bin/kubesolo

VOLUME ["/var/lib/kubesolo"]

EXPOSE 6443 10250

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/kubesolo"]
CMD ["--container-mode"]
