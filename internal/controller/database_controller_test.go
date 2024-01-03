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

var _ = Describe("Database controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		DatabaseObjectName = "test-db"
		DatabaseNamespace  = "default"
	)

	Context("When updating Database Status", func() {
		It("Should Database has to be created", func() {
			By("By creating a new database")
			ctx := context.Background()
			Database := &postgresv1.Database{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "postgres.aurora.operator.k8s/api/v1",
					Kind:       "Database",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      DatabaseObjectName,
					Namespace: DatabaseNamespace,
				},
				Spec: postgresv1.DatabaseSpec{},
			}
			Expect(k8sClient.Create(ctx, Database)).Should(Succeed())

			Expect(func() error {
				var err error
				// wait up to 60 seconds
				for i := 1; i < 30; i++ {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: Database.Namespace,
						Name:      Database.Name,
					}, Database)
					if err == nil && Database.Status.DatabaseName != "" {
						return nil
					}
					time.Sleep(time.Duration(2*i) * time.Second)
				}
				return fmt.Errorf("timed out with err: %s", err)
			}()).Should(Succeed())
			database := &postgresv1.Database{}
			k8sClient.Get(ctx, types.NamespacedName{
				Namespace: DatabaseNamespace,
				Name:      DatabaseObjectName,
			}, database)
			Expect(database.Status).To(HaveField("DatabaseName", "default_test_db"))
			Expect(database.Status).To(HaveField("AppRoleRW", "default_test_db_role_rw"))
			Expect(database.Status).To(HaveField("AppRoleRO", "default_test_db_role_ro"))
		})
	})
})
