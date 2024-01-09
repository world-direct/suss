package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gprossliner/xhdl"
	"github.com/world-direct/suss"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

var (
	fBindAddress    string
	fKubeConfig     string
	fNodeName       string
	fLeaseNamespace string

	service suss.Service
)

func main() {
	flag.StringVar(&fBindAddress, "bindAddress", "localhost:9993", "address to bind http socket")
	flag.StringVar(&fKubeConfig, "kubeconfig", "", "kubeconfig to use, if not set InClusterConfig is used, can be set by KUBECONFIG envvar")
	flag.StringVar(&fNodeName, "nodename", "", "the name of the node running the service. Can be set by NODE_NAME envvar")
	flag.StringVar(&fLeaseNamespace, "leasenamespace", "", "the namespace for the lease, can be set by the NAMESPACE envvar")

	// klog.InitFlags(flag.CommandLine)
	flag.Parse()

	err := xhdl.Run(initService)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	http.HandleFunc("/version", cmdVersion)
	http.HandleFunc("/healthz", cmdHealthz)

	registerCommand("synchronize", func(ctx xhdl.Context) { service.Synchronize(ctx) })
	registerCommand("teardown", func(ctx xhdl.Context) { service.Teardown(ctx) })
	registerCommand("release", func(ctx xhdl.Context) { service.Release(ctx) })
	registerCommand("releasedelayed", func(ctx xhdl.Context) { service.ReleaseDelayed(ctx) })
	registerCommand("criticalpods", func(ctx xhdl.Context) { service.GetCriticalPods(ctx) })

	klog.Infof("listen on %s\n", fBindAddress)
	http.ListenAndServe(fBindAddress, nil)
}

func registerCommand(name string, fn func(ctx xhdl.Context)) {
	http.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {

		klog.Infof("/%s\n", name)

		// setup own logger to context so that log messages can be
		// written to the client
		defaultlog := klog.FromContext(r.Context())
		sink := defaultlog.GetSink()
		ctxlog := defaultlog.WithSink(rwsink{sink, w})

		myctx := klog.NewContext(r.Context(), ctxlog)

		err := xhdl.RunContext(myctx, func(ctx xhdl.Context) {
			fn(ctx)
		})

		if err != nil {
			klog.Error(err.Error())
			io.WriteString(w, err.Error())
			w.WriteHeader(500)
		}
	})
}

func initService(ctx xhdl.Context) {

	// validate nodename
	if fNodeName == "" {
		fNodeName = os.Getenv("NODENAME")
	}

	if fNodeName == "" {
		ctx.Throw(fmt.Errorf("--nodename arg or NODENAME env var required"))
	}

	// kubeconfig and create Config struct
	k8sConfig := getK8sConfig(ctx)
	k8s := kubernetes.NewForConfigOrDie(k8sConfig)

	// namespace handling
	if fLeaseNamespace == "" {
		fLeaseNamespace = os.Getenv("NAMESPACE")

		if fLeaseNamespace == "" {
			ctx.Throw(fmt.Errorf("--leasenamespace arg or NAMESPACE env var required"))
		}
	}

	klog.Infof("using namespace %s for the Lease", fLeaseNamespace)

	// init options
	opt := suss.SussOptions{
		NodeName:                     fNodeName,
		LeaseNamespace:               fLeaseNamespace,
		K8s:                          k8s,
		ConsiderStatefulSetCritical:  true,
		ConsiderSoleReplicasCritical: false,
	}

	// and create service
	service = suss.NewService(opt)

	// perform start tasks, like delayed release
	service.Start(ctx)
}

func getK8sConfig(ctx xhdl.Context) *rest.Config {
	if fKubeConfig == "" {
		fKubeConfig = os.Getenv("KUBECONFIG")
	}

	if fKubeConfig == "" {
		klog.Infof("using InClusterConfig\n")

		k8sConfig, err := rest.InClusterConfig()
		ctx.Throw(err)
		return k8sConfig

	} else {
		klog.Infof("using configfile %s\n", fKubeConfig)

		k8sConfig, err := clientcmd.BuildConfigFromFlags("", fKubeConfig)
		ctx.Throw(err)
		return k8sConfig

	}
}

func cmdVersion(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, VERSION+"\n")
}

func cmdHealthz(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "OK\n")
}
