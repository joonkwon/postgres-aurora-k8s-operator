package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

var _ = Context("When updating DBUser Status", Ordered, func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	var (
		DBUser                                   = "mytestuser"
		Namespace                                = "default"
		DatabaseObjectName                       = "myapp-db2"
		Permission         postgresv1.Permission = "ReadWrite"
	)

	BeforeAll(func() {
		database := &postgresv1.Database{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DatabaseObjectName,
				Namespace: Namespace,
			},
		}
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, database)).Should(Succeed())
		database = &postgresv1.Database{}
		Expect(getDatabaseObject(Namespace, DatabaseObjectName, database)).Should(Succeed())
	})

	Context("When DBUser object is create", Ordered, func() {
		It("Should be created", func() {
			ctx := context.Background()
			dbuser := &postgresv1.DBUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      DBUser,
					Namespace: Namespace,
				},
				Spec: postgresv1.DBUserSpec{
					Database: postgresv1.DatabaseNamespaceName{
						Namespace: Namespace,
						Name:      DatabaseObjectName,
					},
					Permission: Permission,
				},
			}
			Expect(k8sClient.Create(ctx, dbuser)).Should(Succeed())
			dbuserRet := &postgresv1.DBUser{}
			Expect(func() error {
				var err error
				// wait up to 40 seconds
				for i := 1; i < 7; i++ {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: dbuser.Namespace,
						Name:      dbuser.Name,
					}, dbuserRet)
					if err == nil && dbuserRet.Status.UserName != "" && dbuserRet.Status.Database != "" {
						return nil
					}
					time.Sleep(time.Duration(2*i) * time.Second)
				}
				return fmt.Errorf("timed out whith err: %s", err)
			}()).Should(Succeed())

			Expect(dbuserRet.Status).To(HaveField("UserName",
				strings.Replace(fmt.Sprintf("%s_%s", dbuser.Namespace, dbuser.Name), "-", "_", -1)))
			var database = &postgresv1.Database{}
			Expect(getDatabaseObject(dbuserRet.Spec.Database.Namespace, dbuserRet.Spec.Database.Name, database)).Should(Succeed())
			Expect(dbuserRet.Status).To(HaveField("Database", database.Status.DatabaseName))
		})

		It("Should configure finalizer on Database object", func() {
			database := &postgresv1.Database{}
			Expect(getDatabaseObject(Namespace, DatabaseObjectName, database)).Should(Succeed())
			Expect(controllerutil.ContainsFinalizer(database, dbUserFinalizer)).To(BeTrue())
		})
	})

	Context("When DBUser is deleted", Ordered, func() {
		It("Should delete DBUser", func() {
			ctx := context.Background()
			dbuser := &postgresv1.DBUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      DBUser,
					Namespace: Namespace,
				},
			}

			Expect(k8sClient.Delete(ctx, dbuser)).Should(Succeed())

			dbUserDeleted := &postgresv1.DBUser{}
			Expect(func() error {
				var err error
				// loop until an error is raised
				for i := 1; i < 10; i++ {
					err = k8sClient.Get(ctx, types.NamespacedName{
						Namespace: dbuser.Namespace,
						Name:      dbuser.Name,
					}, dbUserDeleted)
					if err != nil {
						return err
					}
					time.Sleep(2 * time.Second)
				}
				return errors.New("time out error")
			}()).Should(MatchError(ContainSubstring("not found")))
		})

		It("Should remove DBuser finalizer from Database", func() {
			var database = &postgresv1.Database{}
			Expect(getDatabaseObject(Namespace, DatabaseObjectName, database)).Should(Succeed())
			// sometime we have to wait enough until the status on K8s control plane get fully updated
			timeout := 240
			Expect(eventually(controllerutil.ContainsFinalizer, timeout, database, dbUserFinalizer)).ShouldNot(BeTrue())
		})
	})

})

type finalizerCheck func(client.Object, string) bool

// eventually wait a certain amount time until a function get the desirable outcome
func eventually(fn finalizerCheck, timeout int, o client.Object, finalizer string) bool {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	out := make(chan bool, 1)
	// retry every second until get true value or time out
	go func(ch chan<- bool, o client.Object, finalizer string) {
		for {
			k8sClient.Get(ctxTimeout, types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      o.GetName(),
			}, o)
			ok := fn(o, finalizer)
			if ok == false {
				out <- false
				break
			}
			time.Sleep(time.Second)
		}
	}(out, o, finalizer)
	select {
	case <-ctxTimeout.Done():
		return true
	case result := <-out:
		return result
	}
}

func getDatabaseObject(namespace, name string, database *postgresv1.Database) error {
	var err error
	ctx := context.Background()
	for i := 1; i < 10; i++ {
		err = k8sClient.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, database)
		if err == nil && database.Status.DatabaseName != "" {
			return nil
		}
		time.Sleep(time.Duration(2*i) * time.Second)
	}
	return fmt.Errorf("time-out whith err: %s", err)
}
