package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

var _ = Describe("Database controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		DatabaseObjectName = "testdb"
		DatabaseNamespace  = "default"
		DatabaseName       = "myapplication_db"
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
				Spec: postgresv1.DatabaseSpec{
					DabaseName: DatabaseName,
				},
			}
			Expect(k8sClient.Create(ctx, Database)).Should(Succeed())
		})
	})
})
