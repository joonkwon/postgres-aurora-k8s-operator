/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	databasev1 "postgres-aurora-db-user/api/v1"
	"postgres-aurora-db-user/internal/services"
)

const (
	databaseFinalizer     = "database.postgres.aurora.operator.k8s/finalizer"
	typeDegradedDatabase  = "Degraded"
	typeAvailableDatabase = "Available"
)

// DatabaseReconciler reconciles a Database object
type DatabaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Postgres *services.PostgresService
}

//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=databases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=databases/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=databases/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Database object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *DatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconcile is running")

	database := &databasev1.Database{}
	if err := r.Get(ctx, req.NamespacedName, database); err != nil {
		log.Error(err, "Unable to fetch Database")

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("found database to reconcile")

	// Let's just set the status as Unknown when no status are available
	if database.Status.Conditions == nil || len(database.Status.Conditions) == 0 {
		meta.SetStatusCondition(&database.Status.Conditions, metav1.Condition{Type: typeAvailableDatabase, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err := r.Status().Update(ctx, database); err != nil {
			log.Error(err, "Failed to update Database status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the database Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, database); err != nil {
			log.Error(err, "Failed to re-fetch Database")
			return ctrl.Result{}, err
		}
	}

	// Set finalizer for database object
	if !controllerutil.ContainsFinalizer(database, databaseFinalizer) {
		log.Info("Adding Finalizer for Database")
		if ok := controllerutil.AddFinalizer(database, databaseFinalizer); !ok {
			log.Error(errors.New("failed to add finalizer into the database resource"), "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err := r.Update(ctx, database); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// check if Database is to be deleted
	isDatabaseMarkedToBeDeleted := database.GetDeletionTimestamp() != nil
	if isDatabaseMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(database, databaseFinalizer) {
			log.Info("Performing Finalizer Operations for database before delete CR")

			// Let's add here an status "Downgrade" to define that this resource begin its process to be terminated.
			meta.SetStatusCondition(&database.Status.Conditions, metav1.Condition{Type: typeDegradedDatabase,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", database.Name)})

			if err := r.Status().Update(ctx, database); err != nil {
				log.Error(err, "Failed to update database status")
				return ctrl.Result{}, err
			}

			// Perform all operations required before remove the finalizer and allow
			// the Kubernetes API to remove the custom resource.
			r.doFinalizerOperationsForDatabase(database)

			// TODO(user): If you add operations to the doFinalizerOperationsFordatabase method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the database Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, database); err != nil {
				log.Error(err, "Failed to re-fetch database")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&database.Status.Conditions, metav1.Condition{Type: typeDegradedDatabase,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", database.Name)})

			if err := r.Status().Update(ctx, database); err != nil {
				log.Error(err, "Failed to update Database status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for database after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(database, databaseFinalizer); !ok {
				err := fmt.Errorf("remove finalizer: %s failed for %s", databaseFinalizer, database.Name)
				log.Error(err, "Failed to remove finalizer for Database")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, database); err != nil {
				log.Error(err, "Failed to remove finalizer for Database")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// create database and ignore database already exists error
	// the database name is `<namespace>_<name>`
	dbname := fmt.Sprintf("%s_%s", database.Namespace, database.Name)
	err := r.Postgres.CreateDB(dbname)
	if err != nil && !services.AlreadyExist(err) {
		log.Error(err, "Failed to create database", "databasename", dbname)
		return ctrl.Result{}, err
	}

	// re-fetch the database
	if err := r.Get(ctx, req.NamespacedName, database); err != nil {
		log.Error(err, "Failed to re-fetch database")
		return ctrl.Result{}, err
	}
	// update DatabaseName in the status
	database.Status.DatabaseName = dbname
	if err := r.Status().Update(ctx, database); err != nil {
		log.Error(err, "Failed to update databaseName in Status", "databasename", dbname)
		return ctrl.Result{}, err
	}

	// create app_rw and app_ro roles
	// and grant neccessary permissions for the database
	// this operations are idempotent

	roleRW, err := r.Postgres.CreateRWRole(dbname)
	if err != nil {
		log.Error(err, "Failed to create RW role for app")
		return ctrl.Result{}, err
	}
	roleRO, err := r.Postgres.CreateRORole(dbname)
	if err != nil {
		log.Error(err, "Failed to create RO role for app")
		return ctrl.Result{}, err
	}

	// update the status of the database
	database.Status.AppRoleRW = roleRW
	database.Status.AppRoleRO = roleRO
	if err := r.Status().Update(ctx, database); err != nil {
		log.Error(err, "Failed to update roles for App in Status", "databasename", dbname)
		return ctrl.Result{}, err
	}

	// Configure Default Privileges (see https://www.postgresql.org/docs/12/sql-alterdefaultprivileges.html)
	if err := r.Postgres.ConfigureDefaultPrivileges(dbname, roleRW, roleRO); err != nil {
		log.Error(err, "Failed to configure Default Previleges")
		return ctrl.Result{}, err
	}

	// update the status of the database
	database.Status.DefaultPrivConfigured = true
	if err := r.Status().Update(ctx, database); err != nil {
		log.Error(err, "Failed to update Default Privilege Configuration Satus", "databasename", dbname)
		return ctrl.Result{}, err
	}

	// Final Status Update
	database.Status.DatabaseName = dbname
	if err := r.Status().Update(ctx, database); err != nil {
		log.Error(err, "Failed to update Databasename Satus", "databasename", dbname)
		return ctrl.Result{}, err
	}

	// re-fetch the database
	if err := r.Get(ctx, req.NamespacedName, database); err != nil {
		log.Error(err, "Failed to re-fetch database")
		return ctrl.Result{}, err
	}
	// update the condition
	meta.SetStatusCondition(&database.Status.Conditions, metav1.Condition{Type: typeAvailableDatabase,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Database creation for custom resource %s name were successfully accomplished", database.Name)})

	if err := r.Status().Update(ctx, database); err != nil {
		log.Error(err, "Failed to update Database status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DatabaseReconciler) doFinalizerOperationsForDatabase(database *databasev1.Database) {
	// TODO: remove database rw role
	// TODO: remove database ro role
	// TODO: check rw user is deleted
	// TODO: check ro user is deleted
}

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1.Database{}).
		Complete(r)
}
