package suss

import (
	"fmt"
	"time"

	"github.com/gprossliner/xhdl"
	"github.com/world-direct/kmutex"
	"github.com/world-direct/looper"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"

	policyv1 "k8s.io/api/policy/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type SussOptions struct {
	NodeName       string
	LeaseNamespace string
	K8s            kubernetes.Interface
}

type service struct {
	km kmutex.Kmutex
	SussOptions
}

type Service interface {
	Start(ctx xhdl.Context)
	Synchronize(ctx xhdl.Context) string
	Teardown(ctx xhdl.Context) string
	Release(ctx xhdl.Context) string
	ReleaseDelayed(ctx xhdl.Context) string
}

const (
	labelPrefix         = "suss.world-direct.at/"
	labelSync           = labelPrefix + "lockowner"
	labelDelayedRelease = labelPrefix + "delayedrelease"
	labelLastRelease    = labelPrefix + "lastrelease"
	labelCriticalPod    = labelPrefix + "critical"
)

func NewService(options SussOptions) Service {

	// init struct
	srv := service{
		SussOptions: options,
		km: kmutex.Kmutex{
			LeaseName:      "sync",
			LeaseNamespace: options.LeaseNamespace,
			HolderIdentity: options.NodeName,
			Clientset:      options.K8s,
			RetryInterval:  time.Second,
		},
	}

	return srv
}

func (srv service) Start(ctx xhdl.Context) {

	// get our node to test the connection and validate the argument
	own := srv.getNodeSet(ctx).OwnNode()
	klog.Infof("Node %s found\n", own.Name())

	testpods := own.CriticalPods2(ctx)
	_ = testpods

	// check for delayed release
	if own.GetLabel(ctx, labelDelayedRelease) == "true" {
		klog.Infof("Node marked for delayed release, releasing lock now")
		srv.Release(ctx)

		own.SetLabel(ctx, labelDelayedRelease, "")
	}
}

func (srv service) Synchronize(ctx xhdl.Context) string {

	looper.Loop(ctx, time.Second*10, func(ctx xhdl.Context) (exit bool) {
		return srv.trySynchronize(ctx)
	})

	return "OK"
}

func (srv service) Teardown(ctx xhdl.Context) string {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	// condon
	own.Cordoned(ctx, true)

	// get pods with critical label
	criticalPods := own.CriticalPods(ctx)
	for _, pod := range criticalPods {
		srv.apiEvictPod(ctx, pod.Namespace, pod.Name)
	}

	// TODO: should loop until no critical pods found

	return "OK"
}

func (srv service) Release(ctx xhdl.Context) string {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	// unset the label
	own.SetLabel(ctx, labelSync, "")

	// write informative label for the user
	own.SetLabel(ctx, labelLastRelease, getTSValue())

	// uncordon
	own.Cordoned(ctx, false)

	return "OK"
}

func (srv service) ReleaseDelayed(ctx xhdl.Context) string {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	own.SetLabel(ctx, labelDelayedRelease, "true")

	return "OK"
}

func getTSValue() string {
	return fmt.Sprintf("%v", time.Now().Unix())
}

func (srv service) apiEvictPod(ctx xhdl.Context, ns string, name string) {
	klog.Infof("Evict Pod %s/%s", ns, name)

	err := srv.K8s.PolicyV1().Evictions(ns).Evict(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	})

	if errors.IsNotFound(err) {
		klog.Infof("Evict failed with NotFound, Pod aready gone %s/%s", ns, name)
		return
	}

	// TODO: we need to make furter tests about possible error codes
	// I didn't manage to produce any of the documented errors, but this may
	// need special pods like some that will need more time to terminate
	ctx.Throw(err)

	// k8s.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	klog.Infof("Pod %s/%s evicted", ns, name)
}

// trys sync one time, returns true if succesful
func (srv service) trySynchronize(ctx xhdl.Context) bool {

	klog.Info("try synchronize")

	if !srv.km.TryAcquire(ctx) {
		return false
	}

	defer srv.km.Release(ctx)

	ns := srv.getNodeSet(ctx)
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
	listOpts.LabelSelector = labelCriticalPod + "=true"

	pods, err := n.srv.K8s.CoreV1().Pods(metav1.NamespaceAll).List(ctx, listOpts)
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

func (n Node) CriticalPods2(ctx xhdl.Context) []v1.Pod {
	listOpts := metav1.ListOptions{}
	listOpts.FieldSelector = fmt.Sprintf("spec.nodeName=%s,status.phase=Running", n.Name())

	pods, err := n.srv.K8s.CoreV1().Pods(metav1.NamespaceAll).List(ctx, listOpts)
	ctx.Throw(err)

	var lst []v1.Pod
	for _, pod := range pods.Items {
		if n.srv.isPodCritical(ctx, &pod) {
			lst = append(lst, pod)
		}
	}
	return lst
}

func (srv service) isPodCritical(ctx xhdl.Context, pod *v1.Pod) bool {

	// check if pod has the critical label explicitly set to true
	if pod.Labels[labelCriticalPod] == "true" {
		klog.Infof("Pod %s/%s is critical %s", pod.Namespace, pod.Name, labelCriticalPod)
		return true
	}

	// check if pod has the critical label explicitly set to false
	if pod.Labels[labelCriticalPod] == "false" {
		klog.Infof("Pod %s/%s is explicitly not critical", pod.Namespace, pod.Name)
		return true
	}

	// check if pod is part of a StatefulSet
	for _, or := range pod.OwnerReferences {
		if srv.isCriticalOwner(ctx, or, pod) {
			return true
		}
	}

	return false
}

func (srv service) isCriticalOwner(ctx xhdl.Context, or metav1.OwnerReference, pod *v1.Pod) bool {

	// check StatefulSet
	if or.APIVersion == appsv1.SchemeGroupVersion.Identifier() && or.Kind == "StatefulSet" {
		klog.Infof("Pod %s/%s is critical (Statefulset)", pod.Namespace, pod.Name)
		return true
	}

	// check ReplicaSet (for Deployments)
	if or.APIVersion == appsv1.SchemeGroupVersion.Identifier() && or.Kind == "ReplicaSet" {

		// with only one replica
		rs, err := srv.K8s.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, or.Name, metav1.GetOptions{})
		ctx.Throw(err)

		if rs.Status.Replicas == 1 {
			klog.Infof("Pod %s/%s is critical (only one replica)", pod.Namespace, pod.Name)
			return true
		} else {
			return false
		}
	}

	return false
}

type NodeSet struct {
	nodes []Node
	srv   service
}

type Node struct {
	node *v1.Node
	srv  service
}

func (n Node) Name() string {
	return n.node.Name
}

func (srv service) getNodeSet(ctx xhdl.Context) NodeSet {
	nodes, err := srv.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	ctx.Throw(err)

	var s []Node
	for i := range nodes.Items {
		np := &nodes.Items[i]
		s = append(s, Node{np, srv})
	}

	return NodeSet{s, srv}
}

func (ns NodeSet) OwnNode() Node {
	return ns.GetNode(ns.srv.NodeName)
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
	nn, err := n.srv.K8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
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
	nn, err := n.srv.K8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	ctx.Throw(err)

	n.node = nn
}
