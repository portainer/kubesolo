# Grafana dashboards for KubeSolo

KubeSolo includes two equivalent Grafana dashboards. Both contain the same panels for:

- API server
- Kubelet
- cAdvisor
- Controller workqueues
- Go runtime

Choose the file that matches your Grafana version:

| File | Format | Recommended use |
|---|---|---|
| `kubesolo-grafana-dashboard-classic.json` | Legacy Grafana dashboard JSON | Recommended for most Grafana OSS, Enterprise, and Grafana Cloud installations |
| `kubesolo-grafana-dashboard-apiv2.json` | `dashboard.grafana.app/v2` | Use only if Dashboard API v2 is enabled and supported by your Grafana instance |

The classic dashboard is the safest default because it works with current Grafana installations.

The v2 dashboard uses Grafana’s newer Kubernetes-style resource model. Instead of storing the dashboard as one opaque JSON document, it represents it as an API resource with fields such as `apiVersion`, `kind`, and `spec`. This format is intended to support better server-side validation, GitOps-friendly diffs, richer variable handling, and easier programmatic editing.

However, Dashboard API v2 is not yet available everywhere. The v2 file is therefore included as a forward-looking option for compatible Grafana instances.

## Importing a dashboard

1. Open Grafana and go to **Dashboards → New → Import**.
2. Upload the desired JSON file, or paste its contents.
3. Select your Prometheus datasource for the `${datasource}` variable.
4. Click **Import**.

The process is the same in Grafana Cloud. Open your stack and select **Dashboards → New → Import**.

If Grafana rejects the v2 file with an error such as `unsupported apiVersion/kind`, your instance does not support Dashboard API v2 yet. In that case, import the classic dashboard instead.

For more information, see Grafana’s documentation on [importing dashboards](https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/import-dashboards/).

## About `dashboard.grafana.app/v2`

Grafana’s Dashboard API v2 is part of its newer App Platform and dashboards-as-code work. It is separate from the legacy dashboard JSON format that Grafana has used for many years.

The format was introduced gradually through versions such as `v2alpha1` and `v2beta1`, initially behind feature toggles including `kubernetesDashboards` and `dashboardNewLayouts`. These features began appearing around Grafana 11.3 and continued into the Grafana 12.x releases.

The v2 file uses the stable-looking `dashboard.grafana.app/v2` resource kind, but it also contains the following value in every panel’s `vizConfig`:

`"version": "13.3.0-35833815807"`

That value identifies a Grafana 13.x development or nightly build. Because the dashboard was exported from a prerelease environment, treat the v2 format as bleeding-edge and check the [current Grafana release notes](https://grafana.com/docs/grafana/latest/whatsnew/) before using it in production.

**Recommendation:** Start with `kubesolo-grafana-dashboard-classic.json`. It is the most compatible option, including for current Grafana Cloud stacks. Switch to the v2 dashboard only after confirming that your Grafana instance supports it and you specifically want its API-driven editing capabilities.

## Shipping metrics with Grafana Alloy

> This part including the `alloy-config.yaml` is merely an example on one of the options to ship your metrics

[`alloy-config.yaml`](alloy-config.yaml) is an example [Grafana Alloy](https://grafana.com/docs/alloy/latest/) configuration that feeds these dashboards. Its `prometheus.scrape "kubesolo"` block collects from exactly two targets, and **these two targets are the basis for every panel in both dashboards**:

- `kubesolo-apiserver` — kube-apiserver's own `/metrics`, proxied through the `kubernetes.default.svc` Service
- `kubesolo-kubelet-cadvisor` — kubelet/cAdvisor metrics, proxied through the apiserver's node proxy (`/api/v1/nodes/<node>/proxy/metrics/cadvisor`)

Everything else in the file — the `ClusterRole`/`ClusterRoleBinding` and the `bearer_token_file`/`tls_config` block — exists solely to authenticate and authorize those two scrapes. The `ClusterRole` grants the Alloy `ServiceAccount` the `nodes`, `nodes/metrics`, `nodes/stats`, and `nodes/proxy` permissions they need (KubeSolo's own `system:kube-apiserver-to-kubelet` role already grants these to the apiserver itself, but Alloy authenticates as its own ServiceAccount, so it needs its own grant).

Apply with:

```sh
kubectl apply -f alloy-config.yaml
```

Then set `GRAFANA_CLOUD_URL`, `GRAFANA_CLOUD_USERNAME`, and `GRAFANA_CLOUD_API_KEY` on the Alloy deployment (or point `prometheus.remote_write` at any other Prometheus-compatible endpoint).

Plain environment variables are the simplest way to get started, but they place the Grafana Cloud username/API key directly on the Alloy Deployment/Pod spec. Where possible, prefer reading them from a Kubernetes `Secret` via [`remote.kubernetes.secret`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.secret/) instead — this keeps credentials out of the Alloy config and off the Pod spec entirely. Non-secret values, such as the remote-write URL, can similarly be sourced from a `ConfigMap` via [`remote.kubernetes.configmap`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.configmap/). For example:

```alloy
remote.kubernetes.secret "grafanacloud" {
  namespace = "monitoring"
  name      = "grafanacloud-credentials"
}

remote.kubernetes.configmap "grafanacloud" {
  namespace = "monitoring"
  name      = "grafanacloud-config"
}

prometheus.remote_write "grafanacloud" {
  endpoint {
    url = remote.kubernetes.configmap.grafanacloud.data["url"]

    basic_auth {
      username = remote.kubernetes.secret.grafanacloud.data["username"]
      password = remote.kubernetes.secret.grafanacloud.data["api-key"]
    }
  }
}
```

For installation options (Helm chart, binary, Docker) and the full `prometheus.scrape`/`prometheus.remote_write`/`discovery.kubernetes` component reference, see the [Grafana Alloy documentation](https://grafana.com/docs/alloy/latest/).