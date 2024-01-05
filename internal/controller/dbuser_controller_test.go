package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

var _ = Context("When updating DBUser Status", Ordered, func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	var (
		DBUser                                   = "testuser"
		Namespace                                = "default"
		DatabaseObjectName                       = "myapp-db"
		Permission         postgresv1.Permission = "ReadWrite"
		database                                 = &postgresv1.Database{}
	)

	BeforeAll(func() {
		database = &postgresv1.Database{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DatabaseObjectName,
				Namespace: Namespace,
			},
		}
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, database)).Should(Succeed())
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
			Expect(func() error {
				var err error
				// wait up to 40 seconds
				for i := 1; i < 7; i++ {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: dbuser.Namespace,
						Name:      dbuser.Name,
					}, dbuser)
					if err == nil && dbuser.Status.UserName != "" && dbuser.Status.Database != "" {
						return nil
					}
					time.Sleep(time.Duration(2*i) * time.Second)
				}
				return fmt.Errorf("timed out whith err: %s", err)
			}()).Should(Succeed())

			Expect(dbuser.Status).To(HaveField("UserName",
				strings.Replace(fmt.Sprintf("%s_%s", dbuser.Namespace, dbuser.Name), "-", "_", -1)))
			Expect(dbuser.Status).To(HaveField("Database", database.Status.DatabaseName))
		})

		It("Should configure finalizer on Database object", func() {
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

		It("Should remove DBuser finalizer form Database", func() {
			Expect(getDatabaseObject(Namespace, DatabaseObjectName, database)).Should(Succeed())
			Expect(controllerutil.ContainsFinalizer(database, dbUserFinalizer)).ShouldNot(BeTrue())
		})
	})

})

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
