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
	Clientset                  kubernetes.Interface
	RetryInterval              time.Duration
}

// withLease is a helper for getting the Lease and retry loop
// if returns ok if the fn succeeded
func (km Kmutex) withLease(ctx xhdl.Context, fn func(ctx xhdl.Context, lease *coordinationv1.Lease) bool) bool {
	li := km.Clientset.CoordinationV1().Leases(km.LeaseNamespace)

	for {
		lease, err := li.Get(ctx, km.LeaseName, metav1.GetOptions{})
		if err != nil {
			if km.DontCreateLeaseIfNotExists {
				ctx.Throw(err)
			}

			if errors.IsNotFound(err) {
				klog.Infof("Lease %v/%v not found, creating", km.LeaseNamespace, km.LeaseName)

				// the fake.NewSimpleClientset() doesn't create a new instance, like the
				// real kubernetes.Clientset, so we create it here if it is nil to
				// make the test work
				if lease == nil {
					lease = &coordinationv1.Lease{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Lease",
							APIVersion: "coordination.k8s.io/v1",
						},
					}
				}

				lease.Name = km.LeaseName
				lease.Namespace = km.LeaseNamespace

				lcreated, err := li.Create(ctx, lease, metav1.CreateOptions{})
				ctx.Throw(err)
				lease = lcreated
			}
		}

		result := fn(ctx, lease)

		// save lease
		_, err = li.Update(ctx, lease, metav1.UpdateOptions{})

		if err != nil {

			// we retry the operation in case of conflict
			// because the lease is loaded again, we should be able to resolve this
			if !errors.IsConflict(err) {
				ctx.Throw(err)
			} else {
				// if conflict we will stay in retry loop
				time.Sleep(km.RetryInterval)
				continue
			}
		}

		return result

	}
}

func (km Kmutex) CurrentOwner(ctx xhdl.Context) (owner string) {

	km.withLease(ctx, func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool) {
		if lease.Spec.HolderIdentity == nil {
			owner = ""
		} else {
			owner = *lease.Spec.HolderIdentity
		}

		return false
	})

	return
}

func (km Kmutex) TryAcquire(ctx xhdl.Context) bool {

	return km.withLease(ctx, func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool) {

		// check if lease not owned
		if lease.Spec.HolderIdentity == nil {
			lease.Spec.HolderIdentity = &km.HolderIdentity
			return true
		}

		// check if we own
		if *lease.Spec.HolderIdentity == km.HolderIdentity {
			return true
		}

		// other owner, unable to acquire
		return false
	})

}

func (km Kmutex) Release(ctx xhdl.Context) {
	km.withLease(ctx, func(ctx xhdl.Context, lease *coordinationv1.Lease) (retry bool) {
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != km.HolderIdentity {
			panic("release a lock not held")
		}

		lease.Spec.HolderIdentity = nil
		return true
	})
}
