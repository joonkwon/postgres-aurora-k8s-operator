package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

// var _ = Describe("DBUser controller", func() {

var _ = Context("When updating DBUser Status", Ordered, func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	var (
		DBUser                                   = "testuser"
		Namespace                                = "default"
		DatabaseObjectName                       = "myapp-db"
		Permission         postgresv1.Permission = "ReadWrite"
	)

	BeforeAll(func() {
		var database = &postgresv1.Database{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DatabaseObjectName,
				Namespace: Namespace,
			},
		}
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, database)).Should(Succeed())
		Expect(func() error {
			var err error
			var database = &postgresv1.Database{}
			// wait up to 40 seconds
			for i := 1; i < 10; i++ {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: Namespace,
					Name:      DatabaseObjectName,
				}, database)
				if err == nil && database.Status.DatabaseName != "" {
					return nil
				}
				time.Sleep(time.Duration(2*i) * time.Second)
			}
			return fmt.Errorf("timed out whith err: %s", err)
		}()).Should(Succeed())
	})

	It("Should DBUser has to be created", func() {
		By("By creating a new DBUser")
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
				if err == nil && dbuser.Status.UserName != "" {
					return nil
				}
				time.Sleep(time.Duration(2*i) * time.Second)
			}
			return fmt.Errorf("timed out whith err: %s", err)
		}()).Should(Succeed())
	})

	It("Should DBUser has to be removed", func() {
		By("By deleting a DBUser")
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
})

// })
