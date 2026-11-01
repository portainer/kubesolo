# Grafana dashboards for KubeSolo

KubeSolo includes two Grafana dashboards with the same panels and visualizations. They cover:

- API server metrics
- Kubelet metrics
- Controller workqueue metrics
- Go runtime metrics
- KubeSolo-specific panels: component health, certificates, Kine DB size, uptime, and build info

Choose the dashboard format that matches your Grafana installation:

| File | Format | Recommended use |
|---|---|---|
| `kubesolo-grafana-dashboard-classic.json` | Legacy Grafana dashboard JSON | Recommended for most Grafana OSS, Enterprise, and Grafana Cloud installations |
| `kubesolo-grafana-dashboard-apiv2.json` | `dashboard.grafana.app/v2` | Use only if your Grafana instance supports Dashboard API v2 |

The classic dashboard is the recommended default because it is compatible with current Grafana installations.

The v2 dashboard uses Grafana’s newer Kubernetes-style resource model. Rather than representing the dashboard as a single opaque JSON document, it defines it as an API resource with fields such as `apiVersion`, `kind`, and `spec`. This approach is intended to provide better server-side validation, GitOps-friendly diffs, richer variable handling, and easier programmatic editing.

Dashboard API v2 is not available on every Grafana instance yet, so the v2 dashboard is provided as an optional, forward-looking format for compatible installations.

## Importing a dashboard

1. In Grafana, go to **Dashboards → New → Import**.
2. Upload the dashboard JSON file or paste its contents.
3. Select your Prometheus datasource for the `${datasource}` variable.
4. Click **Import**.

The same process applies to Grafana Cloud. Open your stack and go to **Dashboards → New → Import**.

If Grafana rejects the v2 dashboard with an error such as `unsupported apiVersion/kind`, your instance does not support Dashboard API v2. Import the classic dashboard instead.

For more information, see Grafana’s documentation on [importing dashboards](https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/import-dashboards/).

## About `dashboard.grafana.app/v2`

Grafana Dashboard API v2 is part of Grafana’s newer App Platform and dashboards-as-code work. It is separate from the legacy dashboard JSON format that Grafana has used for many years.

The format was introduced gradually through versions such as `v2alpha1` and `v2beta1`. Early versions were available behind feature toggles including `kubernetesDashboards` and `dashboardNewLayouts`, beginning around Grafana 11.3 and continuing through the Grafana 12.x releases.

## Shipping metrics with Grafana Alloy

> This section and [`alloy-config.yaml`](alloy-config.yaml) show one possible way to ship KubeSolo metrics. You can use any metrics agent that fits your environment.

[`alloy-config.yaml`](alloy-config.yaml) is a complete, self-contained manifest that runs [Grafana Alloy](https://grafana.com/docs/alloy/latest/) inside the KubeSolo cluster and sends metrics to a Prometheus-compatible endpoint. It contains a `ServiceAccount`, `ClusterRole`, `ClusterRoleBinding`, `ConfigMap`, and `Deployment`. Everything goes into the `default` namespace, so no namespace needs to exist beforehand.

The Alloy config scrapes two targets. These provide the data used by every panel in both dashboards:

- `kubesolo-apiserver` — the kube-apiserver’s own `/metrics` endpoint, accessed through the `kubernetes.default.svc` Service
- `kubesolo-metrics` — KubeSolo’s own metrics server on `127.0.0.1:9105` (the `kubesolo_*` metrics). Start KubeSolo with `KUBESOLO_METRICS_SERVER=true` to enable it.

The `ClusterRole` grants the Alloy `ServiceAccount` access to the `/metrics` non-resource URL.

### Creating the credentials Secret

The Deployment reads `GRAFANA_CLOUD_URL`, `GRAFANA_CLOUD_USERNAME`, and `GRAFANA_CLOUD_API_KEY` from a Secret named `grafana-cloud-credentials`. The Secret is deliberately not part of `alloy-config.yaml`, so re-applying the file never overwrites your real token with a placeholder. Create it **before** applying the manifest. Without it the pod fails with `CreateContainerConfigError`.

```sh
podman exec kubesolo kubectl create secret generic grafana-cloud-credentials \
  --from-literal=GRAFANA_CLOUD_URL='https://prometheus-prod-XX-prod-REGION.grafana.net/api/prom/push' \
  --from-literal=GRAFANA_CLOUD_USERNAME='1234567' \
  --from-literal=GRAFANA_CLOUD_API_KEY='<your-real-token>' \
  --dry-run=client -o yaml | podman exec -i kubesolo kubectl apply -f -
```

Replace the placeholders with the values from your Grafana Cloud stack. Then apply the manifest:

```sh
podman exec -i kubesolo kubectl apply -f - < alloy-config.yaml
```

To send metrics to another Prometheus-compatible endpoint, change the three values in the Secret.

### Why Alloy uses `hostNetwork`

The Deployment sets `hostNetwork: true` and `dnsPolicy: ClusterFirstWithHostNet` only for KubeSolo’s own metrics on port 9105. KubeSolo binds `127.0.0.1:9105` by default (`metrics.bindAddress`), which is the loopback of the KubeSolo container. A normal pod would get connection refused. With `hostNetwork` the pod shares the node (the KubeSolo container) network namespace, so Alloy sees the same `127.0.0.1:9105` as `podman exec kubesolo wget ...`. `ClusterFirstWithHostNet` keeps in-cluster DNS working for `kubernetes.default.svc`. The apiserver scrape doesn’t need `hostNetwork`.

The alternative is to start KubeSolo with `-e KUBESOLO_METRICS_BIND_ADDRESS=0.0.0.0:9105` and scrape the node `InternalIP` instead, which keeps Alloy on the pod network. This demo keeps the loopback default and `hostNetwork` so the metrics endpoint is never exposed on the container network and the target doesn’t depend on the node IP. The cost is that the Alloy pod shares the node network namespace. That is acceptable for a single-node local demo, but not for a shared cluster without thought.

The image tag is `grafana/alloy:latest` for convenience. Pin a version for anything beyond a demo.

Using a Secret with `envFrom` keeps credentials out of the Alloy configuration. You can also read them from within Alloy using [`remote.kubernetes.secret`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.secret/).

For example, using the same Secret:

```alloy
remote.kubernetes.secret "grafanacloud" {
  namespace = "default"
  name      = "grafana-cloud-credentials"
}

prometheus.remote_write "grafanacloud" {
  endpoint {
    url = remote.kubernetes.secret.grafanacloud.data["GRAFANA_CLOUD_URL"]

    basic_auth {
      username = remote.kubernetes.secret.grafanacloud.data["GRAFANA_CLOUD_USERNAME"]
      password = remote.kubernetes.secret.grafanacloud.data["GRAFANA_CLOUD_API_KEY"]
    }
  }
}
```

Non-sensitive values, such as the remote-write URL, can instead be loaded from a `ConfigMap` with [`remote.kubernetes.configmap`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.configmap/), using the same `data["key"]` access.

For installation options, including the Helm chart, binary, and Docker image, as well as the complete `prometheus.scrape`, `prometheus.remote_write`, and `discovery.kubernetes` component references, see the [Grafana Alloy documentation](https://grafana.com/docs/alloy/latest/).

## Shipping metrics with the OpenTelemetry Collector

> Like the Alloy example, this is only one option for shipping metrics. Choose the agent that best fits your stack.

[`otel-collector-config.yaml`](otel-collector-config.yaml) provides the same setup using the vendor-neutral [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) instead of Alloy.

It scrapes the same two targets (`kubesolo-apiserver` and `kubesolo-metrics`) using the same authentication settings. It then sends the metrics to the same type of Prometheus-compatible endpoint, so it feeds both dashboards in the same way.

Unlike `alloy-config.yaml`, this file has no Deployment. The `kubesolo-metrics` job scrapes `127.0.0.1:9105`, so run the Collector pod with `hostNetwork: true` and `dnsPolicy: ClusterFirstWithHostNet` (Helm: `hostNetwork: true`). See [Why Alloy uses `hostNetwork`](#why-alloy-uses-hostnetwork) for the reasoning and the alternative. The ConfigMap has no `namespace:`, so apply it into the namespace of your Collector release (`kubectl apply -n <ns> -f ...`), and adjust the `ClusterRoleBinding` subject namespace (`default`) to match.

Use this option if you prefer to standardize on the OpenTelemetry Collector rather than run Grafana Alloy.

The OpenTelemetry Collector’s [`prometheusreceiver`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/prometheusreceiver) uses Prometheus’s scrape manager. As a result, its scrape configuration— including `scheme`, `bearer_token_file`, `tls_config`, `metrics_path`, and targets—closely matches Alloy’s `prometheus.scrape` configuration. The main difference is that it is written in YAML instead of Alloy’s configuration language.

The [`prometheusremotewriteexporter`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/prometheusremotewriteexporter) uses the same remote-write protocol as Alloy. Authentication is configured through a [`basicauthextension`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/extension/basicauthextension), which is referenced by the exporter’s `auth.authenticator` setting.

**Important:** the `prometheus` receiver, `prometheusremotewrite` exporter, and `basicauth` extension are available only in the **contrib** distribution. They are not included in the core `otel/opentelemetry-collector` image.

Using the core image causes the Collector to fail at startup with an `unknown component type` error for these components. If you are using the [OpenTelemetry Collector Helm chart](https://github.com/open-telemetry/opentelemetry-helm-charts/tree/main/charts/opentelemetry-collector), configure it to use:

```yaml
image:
  repository: otel/opentelemetry-collector-contrib

command:
  name: otelcol-contrib
```

Apply the configuration with:

```sh
kubectl apply -f otel-collector-config.yaml
```

Then set `GRAFANA_CLOUD_URL`, `GRAFANA_CLOUD_USERNAME`, and `GRAFANA_CLOUD_API_KEY` on the Collector Deployment. Alternatively, configure `exporters.prometheusremotewrite.endpoint` to use another Prometheus-compatible endpoint.

The configuration reads these values using `${env:VAR}`. You can therefore provide them through the container’s `env` or `envFrom` settings, using `secretKeyRef` and `configMapKeyRef` as needed.

Unlike Alloy, the standard OpenTelemetry Collector does not provide equivalent `remote.kubernetes.secret` or `remote.kubernetes.configmap` components. Injecting values through environment variables sourced from Kubernetes Secrets and ConfigMaps is the standard approach for keeping credentials out of the Collector ConfigMap.

Before connecting the pipeline to Grafana Cloud, you can verify that metrics are being collected by temporarily adding a [`debug`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/debugexporter) exporter to the metrics pipeline. Then check the Collector logs for scraped samples.

You can also monitor the Collector’s own `/metrics` endpoint for:

- `otelcol_receiver_accepted_metric_points`
- `otelcol_exporter_sent_metric_points`

For installation options, including the Helm chart, binary, and Docker image, and for the complete component reference, see the [OpenTelemetry Collector documentation](https://opentelemetry.io/docs/collector/).