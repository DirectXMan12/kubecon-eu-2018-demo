package main

import (
	"fmt"
	"time"
	"net/http"
	"net"
	"log"
	"os"
	"strings"
	"math/rand"
	"strconv"
	"io"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"

	kclient "k8s.io/client-go/kubernetes"
	kcorelisters "k8s.io/client-go/listers/core/v1"
	kinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	utilwait "k8s.io/apimachinery/pkg/util/wait"
)

type endpointsBalancer struct {
	eps  kcorelisters.EndpointsLister
	svcs kcorelisters.ServiceLister
}

var (
	resolveLatency = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Subsystem: "balancer",
		Name: "resolve_latency_seconds",
		Help: "time taken to resolve a service to an endpoint IP",
	}, []string{"service", "namespace"})
	requestLatency = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Subsystem: "balancer",
		Name: "request_latency_seconds",
		Help: "time taken to perform a request",
	}, []string{"service", "namespace"})

	baseDomain = ".balance.local"
	servePort = 80
)

func (b *endpointsBalancer) addrsForServicePort(svcName types.NamespacedName, port int32) ([]string, int32, error) {
	start := time.Now()
	defer func() {
		duration := time.Now().Sub(start).Seconds()
		resolveLatency.With(prometheus.Labels{
			"service": svcName.Name,
			"namespace": svcName.Namespace,
		}).Observe(duration)
	}()


	svc, err := b.svcs.Services(svcName.Namespace).Get(svcName.Name)
	if err != nil {
		return nil, 0, err
	}
	var targetPort *string
	for _, portInfo := range svc.Spec.Ports {
		if portInfo.Port == port {
			targetPort = &portInfo.Name
			break
		}
	}

	if targetPort == nil {
		return nil, 0, fmt.Errorf("port %v does not exist on service %s", port, svcName)
	}

	ep, err := b.eps.Endpoints(svcName.Namespace).Get(svcName.Name)
	if err != nil {
		return nil, 0, err
	}

	var addrs []string
	var resPort int32
	for _, subset := range ep.Subsets {
		for _, port := range subset.Ports {
			if port.Name != *targetPort {
				continue
			}
			for _, addr := range subset.Addresses {
				addrs = append(addrs, addr.IP)
			}
			resPort = port.Port
		}
	}

	return addrs, resPort, nil
}

func (b *endpointsBalancer) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		resp.WriteHeader(http.StatusMethodNotAllowed)
		log.Printf("method %s not allowed", req.Method)
		return
	}

	if !strings.HasSuffix(req.Host, baseDomain) {
		resp.WriteHeader(http.StatusNotFound)
		log.Printf("host %q doesn't end with %q", req.Host, baseDomain)
		return
	}

	domainParts := strings.Split(req.Host[:len(req.Host)-len(baseDomain)], ".")
	if len(domainParts) != 2 {
		resp.WriteHeader(http.StatusNotFound)
		log.Printf("domain parts %v not in correct format", domainParts)
		return
	}

	svcName := types.NamespacedName{
		Namespace: domainParts[1],
		Name: domainParts[0],
	}

	// TODO: don't use that cast, use the right version strconv instead
	log.Printf("fetching endpoint IPs for %s:%v", svcName, servePort)
	addrs, targetPort, err := b.addrsForServicePort(svcName, int32(servePort))
	if err != nil {
		log.Printf("error resolve addresses for service %s:%v: %v", svcName, servePort, err)
		resp.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// randomly select an address.  This would be slow due to
	// lock contention in a real-world scenario
	addr := addrs[rand.Intn(len(addrs))]

	backendURL := *req.URL
	backendURL.Host = net.JoinHostPort(addr, strconv.Itoa(int(targetPort)))
	backendURL.Scheme = "http"
	log.Printf("proxying to to %s for %s:%v", backendURL.String(), svcName, servePort)

	backendReq, err := http.NewRequest(http.MethodGet, backendURL.String(), nil)
	if err != nil {
		log.Printf("unable to construct request to endpoint: %v", err)
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}
	backendReq = backendReq.WithContext(req.Context())

	start := time.Now()
	backendResp, err := http.DefaultClient.Do(backendReq)
	if err != nil {
		log.Printf("unable to perform request to endpoint: %v", err)
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}

	duration := time.Now().Sub(start).Seconds()
	requestLatency.With(prometheus.Labels{
		"service": svcName.Name,
		"namespace": svcName.Namespace,
	}).Observe(duration)


	backendHeaders := backendResp.Header
	headers := resp.Header()
	for k, v := range backendHeaders {
		headers[k] = v
	}

	resp.WriteHeader(backendResp.StatusCode)

	_, err = io.Copy(resp, backendResp.Body)
	if err != nil {
		log.Printf("unable to copy from backend response to response: %v", err)
		return
	}
}

func getConfig(kubeConfigPath string) (*rest.Config, error) {
	if kubeConfigPath != "" {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfigPath}
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

		return loader.ClientConfig()
	}

	return rest.InClusterConfig()
}

func main() {
	kubeConfigPath := pflag.StringP("kubeconfig", "c", "", "path to kubeconfig (or empty for in-cluster config")
	pflag.Parse()


	config, err := getConfig(*kubeConfigPath)
	if err != nil {
		log.Fatalf("unable to fetch kubeconfig (in-cluster): %v", err)
	}

	namespace := os.Getenv("WATCHED_NAMESPACE")
	if namespace == "" {
		log.Fatalf("no namespace to watch specified (set the WATCHED_NAMESPACE environment variable, generally via the downward API)")
	}


	clientSet := kclient.NewForConfigOrDie(config)
	informers := kinformers.NewFilteredSharedInformerFactory(clientSet, 20*time.Minute, namespace, nil)

	balancer := &endpointsBalancer{
		eps: informers.Core().V1().Endpoints().Lister(),
		svcs: informers.Core().V1().Services().Lister(),
	}

	go informers.Start(utilwait.NeverStop)

	http.Handle("/", balancer)

	reg := prometheus.NewRegistry()
	reg.MustRegister(requestLatency)
	reg.MustRegister(resolveLatency)
	http.Handle("/_metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", servePort), nil))
}
