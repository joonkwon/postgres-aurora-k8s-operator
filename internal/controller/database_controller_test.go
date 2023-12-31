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
		DatabaseName      = "test-database"
		DatabaseNamespace = "default"
		JobName           = "test-job"
	)

	Context("When updating Database Status", func() {
		It("Should increase CronJob Status.Active count when new Jobs are created", func() {
			By("By creating a new CronJob")
			ctx := context.Background()
			Database := &postgresv1.Database{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "postgres.aurora.operator.k8s/api/v1",
					Kind:       "Database",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      DatabaseName,
					Namespace: DatabaseNamespace,
				},
				Spec: postgresv1.DatabaseSpec{},
			}
			Expect(k8sClient.Create(ctx, Database)).Should(Succeed())
		})
	})
})
