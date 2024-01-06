package kmutex

import (
	"time"

	"github.com/gprossliner/xhdl"
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"k8s.io/client-go/kubernetes"
)

type Kmutex struct {
	LeaseName                  string
	LeaseNamespace             string
	HolderIdentity             string
	DontCreateLeaseIfNotExists bool
	Clientset                  *kubernetes.Clientset
}

func (km Kmutex) withLease(ctx xhdl.Context, fn func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool)) {
	li := km.Clientset.CoordinationV1().Leases(km.LeaseNamespace)

	for {
		lease, err := li.Get(ctx, km.LeaseName, metav1.GetOptions{})
		if err != nil {
			if km.DontCreateLeaseIfNotExists {
				ctx.Throw(err)
			}

			if errors.IsNotFound(err) {
				klog.Infof("Lease %v/%v not found, creating", km.LeaseNamespace, km.LeaseName)
				lease.Name = km.LeaseName
				lease.Namespace = km.LeaseNamespace

				lcreated, err := li.Create(ctx, lease, metav1.CreateOptions{})
				ctx.Throw(err)
				lease = lcreated
			}
		}

		mustRetry := fn(ctx, lease)

		// save lease
		_, err = li.Update(ctx, lease, metav1.UpdateOptions{})

		if err != nil {

			// we retry the operation in case of conflict
			// because the lease is loaded again, we should be able to resolve this
			if !errors.IsConflict(err) {
				ctx.Throw(err)
			} else {
				mustRetry = true
			}
		}

		if !mustRetry {
			return
		}

		// if conflict we will stay in retry loop
		time.Sleep(time.Second)
	}
}

func (km Kmutex) Acquire(ctx xhdl.Context) {

	km.withLease(ctx, func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool) {

		// check if lease not owned
		if lease.Spec.HolderIdentity == nil {
			lease.Spec.HolderIdentity = &km.HolderIdentity
			return false
		}

		// check if we own
		if *lease.Spec.HolderIdentity == km.HolderIdentity {
			return false
		}

		// other owner -> retry
		return true
	})

}

func (km Kmutex) Release(ctx xhdl.Context) {
	km.withLease(ctx, func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool) {
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != km.HolderIdentity {
			panic("release a lock not held")
		}

		lease.Spec.HolderIdentity = nil
		return false
	})
}
