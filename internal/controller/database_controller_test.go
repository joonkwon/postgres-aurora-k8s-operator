package controller

import (
	"context"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1 "postgres-aurora-db-user/api/v1"
	"postgres-aurora-db-user/internal/services"
)

var _ = Describe("Database controller create and delete", Ordered, func() {

	Context("When create database custom resource on K8s", func() {
		var (
			DatabaseName      = "test-db"
			DatabaseNamespace = "default"
			ctx               = context.Background()
		)
		It("Should call create operation successfully", func() {

			database := &postgresv1.Database{
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
			Expect(k8sClient.Create(ctx, database)).Should(Succeed())
		})

		It("Should have the object created with necessary status configured", func() {
			database := &postgresv1.Database{}
			Expect(func() error {
				var err error
				// wait for a while for the object is created
				for i := 1; i < 10; i++ {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: DatabaseNamespace,
						Name:      DatabaseName,
					}, database)
					if err == nil && database.Status.DatabaseName != "" {
						return nil
					}
					time.Sleep(time.Duration(2*i) * time.Second)
				}
				return fmt.Errorf("timed out with err: %s", err)
			}()).Should(Succeed())
			Expect(database.Status).To(HaveField("DatabaseName", "default_test_db"))
			Expect(database.Status).To(HaveField("AppRoleRW", "default_test_db_role_rw"))
			Expect(database.Status).To(HaveField("AppRoleRO", "default_test_db_role_ro"))
			Expect(databaseExist(postgres, database.Status.DatabaseName)).To(BeTrue())
			var nonExistingDB = "not_there"
			Expect(databaseExist(postgres, nonExistingDB)).Error().Should(MatchError("sql: no rows in result set"))
			Expect(roleExist(postgres, database.Status.AppRoleRO)).To(BeTrue())
			Expect(roleExist(postgres, database.Status.AppRoleRW)).To(BeTrue())
			var nonExistingRole = "non_role"
			Expect(roleExist(postgres, nonExistingRole)).Error().Should(MatchError("sql: no rows in result set"))
		})

	})
})

// const (
// 	PSObjectTypeDatabase = "database"
// 	PSObjectTypeRole = "role"
// )
// type PSObjectType string

func databaseExist(p *services.PostgresService, dbname string) (bool, error) {
	dbClient, err := p.GetMgmtDBClient()
	if err != nil {
		return false, err
	}
	stmt := "SELECT datname FROM pg_catalog.pg_database WHERE datname=$1"
	row := dbClient.QueryRow(stmt, dbname)
	if row.Err() != nil {
		return false, err
	}
	var dbnameRet string
	err = row.Scan(&dbnameRet)
	if err != nil {
		return false, err
	}
	return true, nil
}

func roleExist(p *services.PostgresService, roleName string) (bool, error) {
	dbClient, err := p.GetMgmtDBClient()
	if err != nil {
		return false, err
	}
	stmt := "SELECT rolname FROM pg_catalog.pg_roles WHERE rolname=$1"
	row := dbClient.QueryRow(stmt, roleName)
	if row.Err() != nil {
		return false, err
	}
	var roleNameRet string
	err = row.Scan(&roleNameRet)
	if err != nil {
		return false, err
	}
	return true, nil
}
