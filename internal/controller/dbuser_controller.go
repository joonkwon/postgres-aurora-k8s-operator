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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	postgresv1 "postgres-aurora-db-user/api/v1"
)

const (
	dbUserFinalizer     = "database.postgres.aurora.operator.k8s/finalizer"
	typeDegradedDBUser  = "Degraded"
	typeAvailableDBUser = "Available"
)

// DBUserReconciler reconciles a DBUser object
type DBUserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DBUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1.DBUser{}).
		Complete(r)
}

func databaseIsReady(database postgresv1.Database) bool {
	// TODO: implement check for condition in Status
	if database.Status.AppRoleRO == "" || database.Status.AppRoleRW == "" ||
		database.Status.DatabaseName == "" {
		return false
	}
	return true
}
