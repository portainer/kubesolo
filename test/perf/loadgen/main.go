// Command loadgen drives API server and datastore load against a KubeSolo
// cluster for the performance suite. Each scenario prints one JSON object on
// stdout; the shell harness gates on its fields.
//
// Usage: loadgen <scenario> [flags]
//
//	churn  create/update/delete ConfigMaps at a target rate (kine write path)
//	watch  many watchers on one namespace while a writer updates (watch fan-out)
//	pods   create and delete Deployments in waves (scheduler, kubelet, runtime)
//	fill   write objects and revisions to grow the datastore
//	list   time full unpaged consistent LISTs
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
)

const (
	managedLabel = "kubesolo-perf"
	sentAtAnno   = "kubesolo-perf/sent-at"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadgen <churn|watch|pods|fill|list> [flags]")
		os.Exit(2)
	}
	scenario := os.Args[1]

	fs := flag.NewFlagSet(scenario, flag.ExitOnError)
	kubeconfig := fs.String("kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig path")
	kubeContext := fs.String("context", "", "kube context")
	namespace := fs.String("namespace", "perf-load", "namespace to work in (must exist)")
	duration := fs.Duration("duration", 2*time.Minute, "how long churn and watch run")
	workers := fs.Int("workers", 4, "concurrent writers (churn, fill)")
	qps := fs.Float64("qps", 50, "target writes per second across all writers (churn, watch)")
	size := fs.Int("size", 1024, "ConfigMap payload bytes")
	watchers := fs.Int("watchers", 100, "concurrent watches (watch)")
	waves := fs.Int("waves", 3, "create/delete waves (pods)")
	replicas := fs.Int("replicas", 20, "pods per wave (pods)")
	image := fs.String("image", "registry.k8s.io/pause:3.10", "pod image (pods)")
	objects := fs.Int("objects", 1000, "distinct ConfigMaps (fill)")
	revisions := fs.Int("revisions", 50000, "total writes, including creates (fill)")
	repeat := fs.Int("repeat", 20, "LIST calls (list)")
	_ = fs.Parse(os.Args[2:])

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = *kubeconfig
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules,
		&clientcmd.ConfigOverrides{CurrentContext: *kubeContext}).ClientConfig()
	if err != nil {
		fatal("load kubeconfig: %v", err)
	}
	// Pace with our own ticker; client-go's default 5 QPS would cap the load.
	cfg.QPS, cfg.Burst = 1000, 2000
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fatal("client: %v", err)
	}

	ctx := context.Background()
	var result any
	switch scenario {
	case "churn":
		result = churn(ctx, cs, *namespace, *duration, *workers, *qps, *size)
	case "watch":
		result = watchStorm(ctx, cs, *namespace, *duration, *watchers, *qps, *size)
	case "pods":
		result = podWaves(ctx, cs, *namespace, *waves, *replicas, *image)
	case "fill":
		result = fill(ctx, cs, *namespace, *workers, *objects, *revisions, *size)
	case "list":
		result = list(ctx, cs, *namespace, *repeat)
	default:
		fatal("unknown scenario %q", scenario)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal("encode result: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loadgen: "+format+"\n", args...)
	os.Exit(1)
}

// latencies collects durations from many goroutines.
type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.d = append(l.d, d)
	l.mu.Unlock()
}

// ms returns the q-quantile in milliseconds, 0 when empty.
func (l *latencies) ms(q float64) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.d) == 0 {
		return 0
	}
	sort.Slice(l.d, func(i, j int) bool { return l.d[i] < l.d[j] })
	i := int(q * float64(len(l.d)-1))
	return float64(l.d[i].Microseconds()) / 1000
}

func configMap(name string, size int) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{managedLabel: "true"}},
		Data:       map[string]string{"payload": strings.Repeat("x", size)},
	}
}

// pace returns a channel that ticks qps times a second until ctx ends.
func pace(ctx context.Context, qps float64) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		t := time.NewTicker(time.Duration(float64(time.Second) / qps))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case ch <- struct{}{}:
				default: // writers are saturated; drop the tick rather than queue
				}
			}
		}
	}()
	return ch
}

type churnResult struct {
	Ops      int64   `json:"ops"`
	Errors   int64   `json:"errors"`
	OpsPerS  float64 `json:"ops_per_s"`
	P50Ms    float64 `json:"p50_ms"`
	P99Ms    float64 `json:"p99_ms"`
	MaxMs    float64 `json:"max_ms"`
	FirstErr string  `json:"first_error,omitempty"`
}

// churn cycles each writer through create → update → delete on its own
// ConfigMaps, the mutating mix a busy controller produces.
func churn(ctx context.Context, cs kubernetes.Interface, ns string, d time.Duration, workers int, qps float64, size int) churnResult {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	cms := cs.CoreV1().ConfigMaps(ns)
	ticks := pace(ctx, qps)

	var lat latencies
	var ops, errs atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				name := fmt.Sprintf("churn-%d-%d", w, i/3)
				var err error
				if _, ok := <-ticks; !ok {
					return
				}
				start := time.Now()
				switch i % 3 {
				case 0:
					_, err = cms.Create(context.Background(), configMap(name, size), metav1.CreateOptions{})
				case 1:
					cm := configMap(name, size)
					cm.Data["i"] = strconv.Itoa(i)
					_, err = cms.Update(context.Background(), cm, metav1.UpdateOptions{})
				case 2:
					err = cms.Delete(context.Background(), name, metav1.DeleteOptions{})
				}
				lat.add(time.Since(start))
				ops.Add(1)
				if err != nil {
					errs.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
			}
		}(w)
	}
	wg.Wait()

	r := churnResult{Ops: ops.Load(), Errors: errs.Load(), P50Ms: lat.ms(0.50), P99Ms: lat.ms(0.99), MaxMs: lat.ms(1)}
	r.OpsPerS = float64(r.Ops) / d.Seconds()
	if e, ok := firstErr.Load().(string); ok {
		r.FirstErr = e
	}
	return r
}

type watchResult struct {
	Watchers     int     `json:"watchers"`
	Writes       int64   `json:"writes"`
	WriteErrors  int64   `json:"write_errors"`
	MinReceived  int64   `json:"min_received"`
	Missed       int64   `json:"missed"`
	Disconnects  int64   `json:"disconnects"`
	LagP50Ms     float64 `json:"lag_p50_ms"`
	LagP99Ms     float64 `json:"lag_p99_ms"`
	LagMaxMs     float64 `json:"lag_max_ms"`
	DeliveredPer float64 `json:"events_delivered_per_s"`
}

// watchStorm opens many watches on one ConfigMap's namespace while a writer
// updates it, stamping each write with its send time. Every watcher must see
// every write; the lag between the stamp and receipt is the fan-out latency.
func watchStorm(ctx context.Context, cs kubernetes.Interface, ns string, d time.Duration, watchers int, qps float64, size int) watchResult {
	cms := cs.CoreV1().ConfigMaps(ns)
	const name = "watch-target"
	created, err := cms.Create(ctx, configMap(name, size), metav1.CreateOptions{})
	if err != nil {
		fatal("create %s: %v", name, err)
	}

	var lat latencies
	var disconnects atomic.Int64
	received := make([]atomic.Int64, watchers)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	ready := sync.WaitGroup{}
	ready.Add(watchers)
	for i := 0; i < watchers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rv := created.ResourceVersion
			first := true
			for {
				w, err := cms.Watch(context.Background(), metav1.ListOptions{
					FieldSelector:   "metadata.name=" + name,
					ResourceVersion: rv,
				})
				if first {
					ready.Done()
					first = false
				}
				if err != nil {
					disconnects.Add(1)
					time.Sleep(time.Second)
					continue
				}
			events:
				for {
					select {
					case <-stop:
						w.Stop()
						return
					case ev, ok := <-w.ResultChan():
						if !ok {
							disconnects.Add(1)
							break events
						}
						cm, isCM := ev.Object.(*corev1.ConfigMap)
						if ev.Type != watch.Modified || !isCM {
							continue
						}
						rv = cm.ResourceVersion
						if ns, err := strconv.ParseInt(cm.Annotations[sentAtAnno], 10, 64); err == nil {
							lat.add(time.Since(time.Unix(0, ns)))
						}
						received[i].Add(1)
					}
				}
			}
		}(i)
	}
	ready.Wait()
	time.Sleep(2 * time.Second) // let the last watch establish before the first write

	var writes, writeErrs atomic.Int64
	wctx, cancel := context.WithTimeout(ctx, d)
	for range pace(wctx, qps) {
		cm := configMap(name, size)
		cm.Annotations = map[string]string{sentAtAnno: strconv.FormatInt(time.Now().UnixNano(), 10)}
		if _, err := cms.Update(context.Background(), cm, metav1.UpdateOptions{}); err != nil {
			writeErrs.Add(1)
			continue
		}
		writes.Add(1)
	}
	cancel()

	// Give stragglers a bounded window to drain before counting.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		done := true
		for i := range received {
			if received[i].Load() < writes.Load() {
				done = false
				break
			}
		}
		if done {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	r := watchResult{Watchers: watchers, Writes: writes.Load(), WriteErrors: writeErrs.Load(), Disconnects: disconnects.Load(),
		LagP50Ms: lat.ms(0.50), LagP99Ms: lat.ms(0.99), LagMaxMs: lat.ms(1)}
	r.MinReceived = r.Writes
	var total int64
	for i := range received {
		n := received[i].Load()
		total += n
		if n < r.MinReceived {
			r.MinReceived = n
		}
	}
	r.Missed = r.Writes*int64(watchers) - total
	r.DeliveredPer = float64(total) / d.Seconds()
	return r
}

type podsResult struct {
	Waves        int     `json:"waves"`
	Replicas     int     `json:"replicas"`
	StartupP50S  float64 `json:"startup_p50_s"`
	StartupP99S  float64 `json:"startup_p99_s"`
	WaveMaxS     float64 `json:"wave_max_s"`
	TeardownMaxS float64 `json:"teardown_max_s"`
}

// podWaves creates a Deployment, waits for every pod to be Ready, records each
// pod's creation → Ready time, deletes it and waits for the pods to go.
func podWaves(ctx context.Context, cs kubernetes.Interface, ns string, waves, replicas int, image string) podsResult {
	r := podsResult{Waves: waves, Replicas: replicas}
	var startup latencies
	labels := map[string]string{managedLabel: "pods"}
	selector := managedLabel + "=pods"
	for wave := 0; wave < waves; wave++ {
		start := time.Now()
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "wave", Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To(int32(replicas)),
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: ptr.To(int64(0)),
						Containers: []corev1.Container{{
							Name: "c", Image: image,
						}},
					},
				},
			},
		}
		if _, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
			fatal("wave %d: create deployment: %v", wave, err)
		}

		var pods []corev1.Pod
		if !poll(5*time.Minute, func() bool {
			list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return false
			}
			pods = list.Items
			return countReady(pods) == replicas
		}) {
			fatal("wave %d: only %d/%d pods Ready after 5m", wave, countReady(pods), replicas)
		}
		r.WaveMaxS = max(r.WaveMaxS, time.Since(start).Seconds())
		for _, p := range pods {
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady {
					startup.add(c.LastTransitionTime.Sub(p.CreationTimestamp.Time))
				}
			}
		}

		start = time.Now()
		if err := cs.AppsV1().Deployments(ns).Delete(ctx, "wave", metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}); err != nil {
			fatal("wave %d: delete deployment: %v", wave, err)
		}
		if !poll(5*time.Minute, func() bool {
			list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			return err == nil && len(list.Items) == 0
		}) {
			fatal("wave %d: pods still present 5m after delete", wave)
		}
		if !poll(time.Minute, func() bool {
			_, err := cs.AppsV1().Deployments(ns).Get(ctx, "wave", metav1.GetOptions{})
			return err != nil
		}) {
			fatal("wave %d: deployment still present 1m after its pods", wave)
		}
		r.TeardownMaxS = max(r.TeardownMaxS, time.Since(start).Seconds())
	}
	r.StartupP50S = startup.ms(0.50) / 1000
	r.StartupP99S = startup.ms(0.99) / 1000
	return r
}

func countReady(pods []corev1.Pod) int {
	n := 0
	for _, p := range pods {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				n++
			}
		}
	}
	return n
}

func poll(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

type fillResult struct {
	Objects  int     `json:"objects"`
	Writes   int64   `json:"writes"`
	Errors   int64   `json:"errors"`
	WritesPS float64 `json:"writes_per_s"`
	Seconds  float64 `json:"seconds"`
}

// fill creates objects ConfigMaps, then updates them round-robin until the
// total number of writes reaches revisions, growing kine's revision history.
func fill(ctx context.Context, cs kubernetes.Interface, ns string, workers, objects, revisions int, size int) fillResult {
	cms := cs.CoreV1().ConfigMaps(ns)
	start := time.Now()
	var next, writes, errs atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := next.Add(1) - 1
				if i >= int64(revisions) {
					return
				}
				name := fmt.Sprintf("fill-%d", i%int64(objects))
				cm := configMap(name, size)
				var err error
				if i < int64(objects) {
					_, err = cms.Create(ctx, cm, metav1.CreateOptions{})
				} else {
					cm.Data["i"] = strconv.FormatInt(i, 10)
					_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
				}
				if err != nil {
					errs.Add(1)
					continue
				}
				writes.Add(1)
			}
		}()
	}
	wg.Wait()
	secs := time.Since(start).Seconds()
	return fillResult{Objects: objects, Writes: writes.Load(), Errors: errs.Load(), WritesPS: float64(writes.Load()) / secs, Seconds: secs}
}

type listResult struct {
	Items int     `json:"items"`
	P50Ms float64 `json:"p50_ms"`
	P99Ms float64 `json:"p99_ms"`
}

// list times unpaged LISTs with an empty resourceVersion: a consistent read,
// which costs a datastore revision check plus encoding every object.
func list(ctx context.Context, cs kubernetes.Interface, ns string, repeat int) listResult {
	var lat latencies
	var items int
	for i := 0; i < repeat; i++ {
		start := time.Now()
		l, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{LabelSelector: managedLabel + "=true"})
		if err != nil {
			fatal("list: %v", err)
		}
		lat.add(time.Since(start))
		items = len(l.Items)
	}
	return listResult{Items: items, P50Ms: lat.ms(0.50), P99Ms: lat.ms(0.99)}
}
