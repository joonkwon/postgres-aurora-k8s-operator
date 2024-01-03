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
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	postgresv1 "postgres-aurora-db-user/api/v1"
	"postgres-aurora-db-user/internal/services"
)

const (
	dbUserFinalizer     = "dbuser.postgres.aurora.operator.k8s/finalizer"
	typeDegradedDBUser  = "Degraded"
	typeAvailableDBUser = "Available"
)

// DBUserReconciler reconciles a DBUser object
type DBUserReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Postgres *services.PostgresService
}

//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=dbusers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=dbusers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=postgres.aurora.operator.k8s,resources=dbusers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DBUser object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *DBUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("DBUser reconcile started")

	dbuser := &postgresv1.DBUser{}
	if err := r.Get(ctx, req.NamespacedName, dbuser); err != nil {
		log.Error(err, "Unable to fetch DBUser")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("found dbuser to reconcile")

	// Let's just set the status as Unknown when no status are available
	if dbuser.Status.Conditions == nil || len(dbuser.Status.Conditions) == 0 {
		meta.SetStatusCondition(&dbuser.Status.Conditions, metav1.Condition{Type: typeAvailableDBUser, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err := r.Status().Update(ctx, dbuser); err != nil {
			log.Error(err, "Failed to update DBUser status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the DBUser Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, dbuser); err != nil {
			log.Error(err, "Failed to re-fetch DBUser")
			return ctrl.Result{}, err
		}
	}

	database := postgresv1.Database{}
	if err := r.Get(ctx, types.NamespacedName(dbuser.Spec.Database), &database); err != nil {
		log.Error(err, "Unable to fetch Database for the DBUser")
		return ctrl.Result{}, err
	}

	if !databaseIsReady(database) {
		err := fmt.Errorf("database is not ready")
		log.Error(err, "for DBUser", "name", database.Name, "namespace", database.Namespace)
		return ctrl.Result{}, err
	}

	// Set finalizer for DBUser object
	if !controllerutil.ContainsFinalizer(dbuser, dbUserFinalizer) {
		log.Info("Adding Finalizer for DBUser")
		if ok := controllerutil.AddFinalizer(dbuser, dbUserFinalizer); !ok {
			log.Error(errors.New("failed to add finalizer into the dbuser resource"), "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err := r.Update(ctx, dbuser); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// set finalize on database for dbuser
	// this will prevent, database being deleted before removing the user
	if !controllerutil.ContainsFinalizer(&database, dbUserFinalizer) {
		log.Info("Adding DBUser Finalizer to Database")
		if ok := controllerutil.AddFinalizer(&database, dbUserFinalizer); !ok {
			log.Error(errors.New("failed to add DBUser finalizer into the database resource"), "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err := r.Update(ctx, &database); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Delete Finalizers for deletion
	isDBUserMarkedToBeDeleted := dbuser.GetDeletionTimestamp() != nil
	if isDBUserMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(dbuser, dbUserFinalizer) {
			meta.SetStatusCondition(&dbuser.Status.Conditions, metav1.Condition{Type: typeDegradedDBUser,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s", dbuser.Name)})

			if err := r.Status().Update(ctx, dbuser); err != nil {
				log.Error(err, "Failed to update DBUser status")
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForDBUser(dbuser)

			// re-fetch dbuser
			if err := r.Get(ctx, req.NamespacedName, dbuser); err != nil {
				log.Error(err, "Failed to re-fetch DBUser")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&dbuser.Status.Conditions, metav1.Condition{Type: typeDegradedDBUser,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", dbuser.Name)})

			if err := r.Status().Update(ctx, dbuser); err != nil {
				log.Error(err, "Failed to update DBUser status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for DBUser after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(dbuser, dbUserFinalizer); !ok {
				err := fmt.Errorf("remove finalizer: %s failed for %s", dbUserFinalizer, dbuser.Name)
				log.Error(err, "Failed to remove finalizer for DBUser")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, dbuser); err != nil {
				log.Error(err, "Failed to remove finalizer for Database")
				return ctrl.Result{}, err
			}

			// Once DBUserFinalizer is removed from DBUser
			// Remove DBUserFinalizer from Database object

			log.Info("Removing DBUser Finalizer from associated database after removing it from DBUser")
			if ok := controllerutil.RemoveFinalizer(&database, dbUserFinalizer); !ok {
				err := fmt.Errorf("remove finalizer: %s failed for %s", dbUserFinalizer, database.Name)
				log.Error(err, "Failed to remove finalizer from Database")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, &database); err != nil {
				log.Error(err, "Failed to update the status of Database")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Set DBUserName
	var dbuserName string = strings.Replace(fmt.Sprintf("%s_%s", dbuser.Namespace, dbuser.Name), "-", "_", -1)
	var roleName string
	if dbuser.Spec.Permission == postgresv1.PermissionReadOnly {
		roleName = database.Status.AppRoleRO
	}
	if dbuser.Spec.Permission == postgresv1.PermissionReadWrite {
		roleName = database.Status.AppRoleRW
	}

	// Create DBUser
	if err := r.Postgres.CreateUser(dbuserName, roleName); err != nil && !services.AlreadyExist(err) {
		log.Error(err, "Unable to create user", "username", dbuserName, "role", roleName)
		return ctrl.Result{}, err
	}
	dbuser.Status.UserName = dbuserName
	if err := r.Status().Update(ctx, dbuser); err != nil {
		log.Error(err, "Failed to update DBUser status")
		return ctrl.Result{}, err
	}

	dbuser.Status.Hostname = r.Postgres.Host
	dbuser.Status.Database = database.Status.DatabaseName
	dbuser.Status.Permission = dbuser.Spec.Permission

	// update the condition
	meta.SetStatusCondition(&dbuser.Status.Conditions, metav1.Condition{Type: typeAvailableDBUser, Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "DBUser created"})

	if err := r.Status().Update(ctx, dbuser); err != nil {
		log.Error(err, "Failed to update DBUser status")
		return ctrl.Result{}, err
	}

	// TODO: add IAM policy creation??? leave it to a separate IRSA creation workflow?

	// postgres role management:
	// https://www.prisma.io/dataguide/postgresql/authentication-and-authorization/role-management
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DBUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1.DBUser{}).
		Complete(r)
}

func (r *DBUserReconciler) doFinalizerOperationsForDBUser(dbuser *postgresv1.DBUser) {
	// TODO:
	// delete the user from Postgres
}

func databaseIsReady(database postgresv1.Database) bool {
	// TODO: implement check for condition in Status
	if database.Status.AppRoleRO == "" || database.Status.AppRoleRW == "" ||
		database.Status.DatabaseName == "" {
		return false
	}
	return true
}
