package suss

import (
	"context"
	"fmt"
	"time"

	"github.com/gprossliner/xhdl"
	"github.com/world-direct/kmutex"
	"github.com/world-direct/looper"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type SussOptions struct {
	NodeName                     string
	LeaseNamespace               string
	ConsiderStatefulSetCritical  bool
	ConsiderSoleReplicasCritical bool
	K8s                          kubernetes.Interface
}

type service struct {
	km kmutex.Kmutex
	SussOptions
}

type Service interface {
	Start(ctx xhdl.Context)
	Synchronize(ctx xhdl.Context)
	Teardown(ctx xhdl.Context)
	Release(ctx xhdl.Context)
	ReleaseDelayed(ctx xhdl.Context)
	GetCriticalPods(ctx xhdl.Context)
	TestFail(ctx xhdl.Context)
}

const (
	labelPrefix         = "suss.world-direct.at/"
	labelDelayedRelease = labelPrefix + "delayedrelease"
	labelLastRelease    = labelPrefix + "lastrelease"
	labelCriticalPod    = labelPrefix + "critical"
	labelPodEvicted     = labelPrefix + "evicted"
)

func NewService(options SussOptions) Service {

	// init struct
	srv := service{
		SussOptions: options,
		km: kmutex.Kmutex{
			LeaseName:      "sync", // if we may have multiple groups in the future we can use different names
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
	infof(ctx, "node %s found\n", own.Name())

	// check for delayed release
	if own.GetLabel(ctx, labelDelayedRelease) == "true" {
		infof(ctx, "node marked for delayed release, releasing lock now")
		srv.Release(ctx)

		own.SetLabel(ctx, labelDelayedRelease, "")
	}
}

// infof is a helper to log to klog and http response
func infof(ctx context.Context, format string, args ...interface{}) {
	klog.FromContext(ctx).Info(fmt.Sprintf(format, args...))
}

func (srv service) Synchronize(ctx xhdl.Context) {
	looper.Loop(ctx, time.Second*10, func(ctx xhdl.Context) (exit bool) {
		return func() bool {

			// this is for information only to notify about existing owner
			// if is not race-free if owned by another node (this is done in TryAcquire),
			// but safe it owned by us
			owner := srv.km.CurrentOwner(ctx)
			if owner == srv.km.HolderIdentity {
				infof(ctx, "lock already owned by us %s", owner)
				return true
			}

			if !srv.km.TryAcquire(ctx) {
				infof(ctx, "could not aquire Lease, currently owned by %s", srv.km.CurrentOwner(ctx))
				return false
			} else {
				infof(ctx, "lease successfully aquired by %s", srv.km.HolderIdentity)
				return true
			}
		}()
	})
}

func (srv service) Teardown(ctx xhdl.Context) {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	// condon
	own.Cordoned(ctx, true)
	infof(ctx, "node %s cordoned", own.Name())

	// get pods with critical label
	criticalPods := own.CriticalPods(ctx)
	for _, pod := range criticalPods {

		infof(ctx, "evict pod %s/%s", pod.Namespace, pod.Name)

		// label as evicted so we don't evict again
		srv.apiLabelPod(ctx, &pod, labelPodEvicted, getTSValue())

		// and evict
		srv.apiEvictPod(ctx, pod.Namespace, pod.Name)
	}

	// loop until no critical pods found
	looper.Loop(ctx, time.Second*10, func(ctx xhdl.Context) (exit bool) {
		infof(ctx, "check if critical pods have exited")
		stillAlive := own.CriticalPodsEvicted(ctx)

		if len(stillAlive) == 0 {
			infof(ctx, "critical pods have exited")
			exit = true
			return
		} else {
			infof(ctx, "waiting for pods to exit:")
			for _, pod := range stillAlive {
				infof(ctx, "* %s/%s", pod.Namespace, pod.Name)
			}
			exit = false
			return
		}

	})

}

func (srv service) Release(ctx xhdl.Context) {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	// validate lock ownership
	owner := srv.km.CurrentOwner(ctx)

	// we continue even if lock not held to ensure uncordoned
	// node after release
	if owner != srv.km.HolderIdentity {
		infof(ctx, "lock is currently not held!")
	} else {
		// release lock
		srv.km.Release(ctx)
		infof(ctx, "lock released")

		// set lastRelease info label
		own.SetLabel(ctx, labelLastRelease, getTSValue())
	}

	// uncordon
	infof(ctx, "node uncordoned")
	own.Cordoned(ctx, false)

}

func (srv service) ReleaseDelayed(ctx xhdl.Context) {
	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	infof(ctx, "set label %s node for delayed release", labelDelayedRelease)
	own.SetLabel(ctx, labelDelayedRelease, "true")
}

// getTSValue returns a timestamp based value for labels
func getTSValue() string {
	return fmt.Sprintf("%v", time.Now().Unix())
}

func (srv service) TestFail(ctx xhdl.Context) {
	for i := 0; i < 5; i++ {
		infof(ctx, "loop %d/%d (%v)", i+1, 5, time.Now())
		time.Sleep(time.Second)
	}

	infof(ctx, "failing now")
	ctx.Throw(fmt.Errorf("this fails"))
}
