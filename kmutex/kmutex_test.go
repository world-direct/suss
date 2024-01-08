package kmutex

import (
	"testing"
	"time"

	"github.com/gprossliner/xhdl"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAquireReleaseWithCreateLease(t *testing.T) {

	cs := fake.NewSimpleClientset()

	km := Kmutex{
		LeaseName:                  "lease",
		LeaseNamespace:             "default",
		HolderIdentity:             "me",
		DontCreateLeaseIfNotExists: false,
		Clientset:                  cs,
		RetryInterval:              time.Second,
	}

	err := xhdl.Run(func(ctx xhdl.Context) {
		assert.True(t, km.TryAcquire(ctx))
		km.Release(ctx)

	})

	assert.NoError(t, err)

}

func TestCantAcquireMutextHold(t *testing.T) {

	cs := fake.NewSimpleClientset()

	task1 := Kmutex{
		LeaseName:                  "lease",
		LeaseNamespace:             "default",
		HolderIdentity:             "task1",
		DontCreateLeaseIfNotExists: false,
		Clientset:                  cs,
		RetryInterval:              time.Second,
	}

	task2 := Kmutex{
		LeaseName:                  "lease",
		LeaseNamespace:             "default",
		HolderIdentity:             "task2",
		DontCreateLeaseIfNotExists: false,
		Clientset:                  cs,
		RetryInterval:              time.Second,
	}

	err := xhdl.Run(func(ctx xhdl.Context) {

		// task1 must be able to acquire
		assert.True(t, task1.TryAcquire(ctx))

		// task2 must not be able to acquire because mutex held by task1
		assert.False(t, task2.TryAcquire(ctx))

		// now task1 releases mutex
		task1.Release(ctx)

		// task2 must be able to acquire because mutex released by task1
		assert.True(t, task2.TryAcquire(ctx))
	})

	assert.NoError(t, err)
}

func TestAquireWhenAlreadyHeldShouldWork(t *testing.T) {

	cs := fake.NewSimpleClientset()

	km := Kmutex{
		LeaseName:                  "lease",
		LeaseNamespace:             "default",
		HolderIdentity:             "me",
		DontCreateLeaseIfNotExists: false,
		Clientset:                  cs,
		RetryInterval:              time.Second,
	}

	err := xhdl.Run(func(ctx xhdl.Context) {

		// acquire once
		assert.True(t, km.TryAcquire(ctx))

		// and once more
		assert.True(t, km.TryAcquire(ctx))

		// release both
		km.Release(ctx)

	})

	assert.NoError(t, err)

}

func TestReleaseLockNotHeldShouldPanic(t *testing.T) {

	assert.Panics(t, func() {
		cs := fake.NewSimpleClientset()

		task1 := Kmutex{
			LeaseName:                  "lease",
			LeaseNamespace:             "default",
			HolderIdentity:             "task1",
			DontCreateLeaseIfNotExists: false,
			Clientset:                  cs,
			RetryInterval:              time.Second,
		}

		task2 := Kmutex{
			LeaseName:                  "lease",
			LeaseNamespace:             "default",
			HolderIdentity:             "task2",
			DontCreateLeaseIfNotExists: false,
			Clientset:                  cs,
			RetryInterval:              time.Second,
		}

		xhdl.Run(func(ctx xhdl.Context) {

			// task1 must be able to acquire
			assert.True(t, task1.TryAcquire(ctx))

			// this should panic as task2 do not own the lock
			task2.Release(ctx)
		})

	})

}

func TestCurrentOwner(t *testing.T) {

	cs := fake.NewSimpleClientset()

	km := Kmutex{
		LeaseName:                  "lease",
		LeaseNamespace:             "default",
		HolderIdentity:             "me",
		DontCreateLeaseIfNotExists: false,
		Clientset:                  cs,
		RetryInterval:              time.Second,
	}

	err := xhdl.Run(func(ctx xhdl.Context) {

		// owner should be "" if not owned
		assert.Equal(t, "", km.CurrentOwner(ctx))

		assert.True(t, km.TryAcquire(ctx))
		assert.Equal(t, km.HolderIdentity, km.CurrentOwner(ctx))

		km.Release(ctx)
		assert.Equal(t, "", km.CurrentOwner(ctx))

	})

	assert.NoError(t, err)

}
