package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

var _ = Describe("DBUser controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		DBUser             = "testuser"
		Namespace          = "default"
		DatabaseObjectName = "testdb"
		Permission         = "ReadWrite"
	)

	// setting up database object
	var database *postgresv1.Database = &postgresv1.Database{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "postgres.aurora.operator.k8s/api/v1",
			Kind:       "Database",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DatabaseObjectName,
			Namespace: Namespace,
		},
		Spec: postgresv1.DatabaseSpec{},
	}

	BeforeEach(func() {
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, database)).Should(Succeed())
		Expect(func() error {
			var err error
			// wait up to 60 seconds
			for i := 1; i < 30; i++ {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: database.Namespace,
					Name:      database.Name,
				}, database)
				if err == nil && database.Status.DatabaseName != "" {
					return nil
				}
				time.Sleep(time.Duration(2*i) * time.Second)
			}
			return fmt.Errorf("timed out whith err: %s", err)
		}()).Should(Succeed())
	})

	Context("When updating DBUser Status", func() {
		It("Should DBUser has to be created", func() {
			By("By creating a new DBUser")
			ctx := context.Background()
			dbuser := &postgresv1.DBUser{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "postgres.aurora.operator.k8s/api/v1",
					Kind:       "DBUser",
				},
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
		})
	})
})
