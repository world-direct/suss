package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gprossliner/xhdl"
	"github.com/world-direct/kmutex"
	"github.com/world-direct/looper"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	k8s *kubernetes.Clientset
	km  kmutex.Kmutex
)

const (
	labelPrefix         = "suss.world-direct.at/"
	labelSync           = labelPrefix + "lockowner"
	labelDelayedRelease = labelPrefix + "delayedrelease"
	labelLastRelease    = labelPrefix + "lastrelease"
)

func main() {
	flag.StringVar(&fBindAddress, "bindAddress", "localhost:9993", "address to bind http socket")
	flag.StringVar(&fKubeConfig, "kubeconfig", "", "kubeconfig to use, if not set InClusterConfig is used, can be set by KUBECONFIG envvar")
	flag.StringVar(&fNodeName, "nodename", "", "the name of the node running the service. Can be set by NODE_NAME envvar")
	flag.StringVar(&fLeaseNamespace, "leasenamespace", "default", "the namespace for the lease")

	// klog.InitFlags(flag.CommandLine)
	flag.Parse()

	klog.Infof("Using namespace %s for the Lease", fLeaseNamespace)

	err := xhdl.Run(initService)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	http.HandleFunc("/version", cmdVersion)
	http.HandleFunc("/healthz", cmdHealthz)

	registerCommand("synchronize", cmdSynchronize)
	registerCommand("teardown", cmdTeardown)
	registerCommand("release", cmdRelease)
	registerCommand("releasedelayed", cmdReleaseDelayed)

	klog.Infof("listen on %s\n", fBindAddress)
	http.ListenAndServe(fBindAddress, nil)
}

func registerCommand(name string, fn func(ctx xhdl.Context) string) {
	http.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {

		klog.Infof("/%s\n", name)

		err := xhdl.RunContext(r.Context(), func(ctx xhdl.Context) {
			response := fn(ctx)
			io.WriteString(w, response)
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
		fNodeName = os.Getenv("NODE_NAME")
	}

	if fNodeName == "" {
		ctx.Throw(fmt.Errorf("--nodename arg or NODE_NAME env var required"))
	}

	// kubeconfig and create Config struct
	k8sConfig := getK8sConfig(ctx)
	k8s = kubernetes.NewForConfigOrDie(k8sConfig)

	// init kmutex
	km = kmutex.Kmutex{
		LeaseName:      "sync",
		LeaseNamespace: fLeaseNamespace,
		HolderIdentity: fNodeName,
		Clientset:      k8s,
	}

	// get our node to test the connection and validate the argument
	node := apiGetNode(ctx, fNodeName)
	klog.Infof("Node %s found\n", node.Name)

	// check for delayed release
	own := getNodeSet(ctx).OwnNode()

	if own.GetLabel(ctx, labelDelayedRelease) == "true" {
		klog.Infof("Node marked for delayed release, releasing lock now")
		cmdRelease(ctx)

		own.SetLabel(ctx, labelDelayedRelease, "")
	}
}

func getK8sConfig(ctx xhdl.Context) *rest.Config {
	if fKubeConfig == "" {
		fKubeConfig = os.Getenv("KUBECONFIG")
	}

	if fKubeConfig == "" {
		klog.Infof("Using InClusterConfig\n")

		k8sConfig, err := rest.InClusterConfig()
		ctx.Throw(err)
		return k8sConfig

	} else {
		klog.Infof("Using configfile %s\n", fKubeConfig)

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

func cmdSynchronize(ctx xhdl.Context) string {

	looper.Loop(ctx, time.Second*10, func(ctx xhdl.Context) (exit bool) {
		klog.Info("retry synchronize")
		return trySynchronize(ctx)
	})

	return "OK"
}

func cmdTeardown(ctx xhdl.Context) string {

	ns := getNodeSet(ctx)
	own := ns.OwnNode()

	// condon
	own.Cordoned(ctx, true)

	// get pods with label wd-suss-critical
	criticalPods := own.CriticalPods(ctx)
	for _, pod := range criticalPods {
		apiEvictPod(ctx, pod.Namespace, pod.Name)
	}

	// should loop until no critical pods found

	return ""
}

func cmdRelease(ctx xhdl.Context) string {

	ns := getNodeSet(ctx)
	own := ns.OwnNode()

	// unset the label
	own.SetLabel(ctx, labelSync, "")

	// write informative label for the user
	own.SetLabel(ctx, labelLastRelease, getTSValue())

	// uncordon
	own.Cordoned(ctx, false)

	return "OK"
}

func cmdReleaseDelayed(ctx xhdl.Context) string {

	ns := getNodeSet(ctx)
	own := ns.OwnNode()

	own.SetLabel(ctx, labelDelayedRelease, "true")

	return "OK"
}

func getTSValue() string {
	return fmt.Sprintf("%v", time.Now().Unix())
}

func apiEvictPod(ctx xhdl.Context, ns string, name string) {
	klog.Infof("Evict Pod %s/%s", ns, name)

	err := k8s.PolicyV1().Evictions(ns).Evict(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	})

	if errors.IsNotFound(err) {
		klog.Infof("Evict failed with NotFound, Pod aready gone %s/%s", ns, name)
		return
	}

	ctx.Throw(err)

	// k8s.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	klog.Infof("Pod %s/%s evicted", ns, name)
}

// trys sync one time, returns true if succesful
func trySynchronize(ctx xhdl.Context) bool {

	km.Acquire(ctx)
	defer km.Release(ctx)

	ns := getNodeSet(ctx)
	own := ns.OwnNode()

	// check if we have the lock already
	if own.GetLabel(ctx, labelSync) != "" {
		klog.Infof("Own Node %s already has the lock, done\n", own.Name())
		return true
	}

	// check other nodes for the label
	for _, n := range ns.nodes {
		if n.GetLabel(ctx, labelSync) != "" {
			klog.Infof("Node %s has the lock, will wait\n", n.Name())
			return false
		}
	}

	// set the label
	own.SetLabel(ctx, labelSync, getTSValue())
	klog.Infof("Own Node %s succefully synchronized", own.Name())

	return true
}

func (n Node) CriticalPods(ctx xhdl.Context) []v1.Pod {
	listOpts := metav1.ListOptions{}
	listOpts.LabelSelector = "wd-suss-critical=true"

	pods, err := k8s.CoreV1().Pods(metav1.NamespaceAll).List(ctx, listOpts)
	ctx.Throw(err)

	var lst []v1.Pod

	// check the pods for the node
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != n.Name() {
			continue
		}

		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		lst = append(lst, pod)
	}

	return lst
}

func apiGetNode(ctx xhdl.Context, hostname string) *v1.Node {
	node, err := k8s.CoreV1().Nodes().Get(ctx, hostname, metav1.GetOptions{})
	ctx.Throw(err)

	return node
}

type NodeSet struct {
	nodes []Node
}

type Node struct {
	node *v1.Node
}

func (n Node) Name() string {
	return n.node.Name
}

func (n Node) ResourceVersion() string {
	return n.node.ResourceVersion
}

func getNodeSet(ctx xhdl.Context) NodeSet {
	nodes, err := k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	ctx.Throw(err)

	var s []Node
	for i := range nodes.Items {
		np := &nodes.Items[i]
		s = append(s, Node{np})
	}

	return NodeSet{s}
}

func (ns NodeSet) OwnNode() Node {
	return ns.GetNode(fNodeName)
}

func (ns NodeSet) GetNode(name string) Node {
	for _, node := range ns.nodes {
		if node.Name() == name {
			return node
		}
	}

	panic("node no longer available, can't continue")
}

// sets the Label for a node. If value is am empty string the label is deleted
func (n *Node) SetLabel(ctx xhdl.Context, name, value string) {
	valuestr := "null"
	if value != "" {
		valuestr = fmt.Sprintf(`"%s"`, value)
	}

	patch := fmt.Sprintf(`{"metadata":{"labels":{"%s":%s}}}`, name, valuestr)

	nobj := n.node
	nn, err := k8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	ctx.Throw(err)

	n.node = nn
}

func (n Node) GetLabel(ctx xhdl.Context, name string) string {
	return n.node.Labels[name]
}

// Cordoned marks the node as schedulable
func (n *Node) Cordoned(ctx xhdl.Context, value bool) {
	valuestr := "false"
	if value {
		valuestr = "true"
	}
	patch := fmt.Sprintf(`{"spec":{"unschedulable":%s}}`, valuestr)

	nobj := n.node
	nn, err := k8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	ctx.Throw(err)

	n.node = nn
}
