# Grafana dashboards for KubeSolo

KubeSolo includes two Grafana dashboards with the same panels and visualizations. They cover:

- API server metrics
- Kubelet metrics
- cAdvisor container metrics
- Controller workqueue metrics
- Go runtime metrics

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

[`alloy-config.yaml`](alloy-config.yaml) contains an example [Grafana Alloy](https://grafana.com/docs/alloy/latest/) configuration for sending metrics to a Prometheus-compatible endpoint.

Its `prometheus.scrape "kubesolo"` block collects metrics from exactly two targets. These targets provide the data used by every panel in both dashboards:

- `kubesolo-apiserver` — the kube-apiserver’s own `/metrics` endpoint, accessed through the `kubernetes.default.svc` Service
- `kubesolo-kubelet-cadvisor` — kubelet and cAdvisor metrics, accessed through the apiserver’s node proxy at `/api/v1/nodes/<node>/proxy/metrics/cadvisor`

The rest of the file configures authentication and authorization for these two scrapes. This includes the `ClusterRole`, `ClusterRoleBinding`, `bearer_token_file`, and `tls_config` settings.

The `ClusterRole` grants the Alloy `ServiceAccount` access to the `nodes`, `nodes/metrics`, `nodes/stats`, and `nodes/proxy` resources. KubeSolo’s existing `system:kube-apiserver-to-kubelet` role grants similar access to the apiserver, but Alloy authenticates as its own `ServiceAccount` and therefore needs a separate grant.

Apply the configuration with:

```sh
kubectl apply -f alloy-config.yaml
```

Then set `GRAFANA_CLOUD_URL`, `GRAFANA_CLOUD_USERNAME`, and `GRAFANA_CLOUD_API_KEY` on the Alloy deployment. Alternatively, configure `prometheus.remote_write` to send metrics to another Prometheus-compatible endpoint.

Using environment variables is the simplest way to get started. However, this places the Grafana Cloud username and API key directly in the Alloy Deployment or Pod specification. Where possible, use a Kubernetes `Secret` with [`remote.kubernetes.secret`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.secret/) instead. This keeps credentials out of the Alloy configuration and away from the Pod specification.

Non-sensitive values, such as the remote-write URL, can similarly be loaded from a `ConfigMap` using [`remote.kubernetes.configmap`](https://grafana.com/docs/alloy/latest/reference/components/remote/remote.kubernetes.configmap/). For example:

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

For installation options, including the Helm chart, binary, and Docker image, as well as the complete `prometheus.scrape`, `prometheus.remote_write`, and `discovery.kubernetes` component references, see the [Grafana Alloy documentation](https://grafana.com/docs/alloy/latest/).

## Shipping metrics with the OpenTelemetry Collector

> Like the Alloy example, this is only one option for shipping metrics. Choose the agent that best fits your stack.

[`otel-collector-config.yaml`](otel-collector-config.yaml) provides the same setup using the vendor-neutral [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) instead of Alloy.

It scrapes the same two targets—`kubesolo-apiserver` and `kubesolo-kubelet-cadvisor`—using the same authentication settings. It then sends the metrics to the same type of Prometheus-compatible endpoint, so it feeds both dashboards in the same way.

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