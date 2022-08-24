// Copyright 2022 Clastix Labs
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kamajiv1alpha1 "github.com/clastix/kamaji/api/v1alpha1"
)

type DataStore struct {
	client client.Client
	// TenantControlPlaneTrigger is the channel used to communicate across the controllers:
	// if a Data Source is updated we have to be sure that the reconciliation of the certificates content
	// for each Tenant Control Plane is put in place properly.
	TenantControlPlaneTrigger TenantControlPlaneChannel
	// ResourceName is the DataStore object that should be watched for changes.
	ResourceName string
}

//+kubebuilder:rbac:groups=kamaji.clastix.io,resources=datastores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kamaji.clastix.io,resources=datastores/status,verbs=get;update;patch

func (r *DataStore) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	ds := kamajiv1alpha1.DataStore{}
	if err := r.client.Get(ctx, request.NamespacedName, &ds); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}

		return reconcile.Result{}, err
	}
	// A Data Source can trigger several Tenant Control Planes and requires a minimum validation:
	// we have to ensure the data provided by the Data Source is valid and referencing an existing Secret object.
	if _, err := ds.Spec.TLSConfig.CertificateAuthority.Certificate.GetContent(ctx, r.client); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "invalid Certificate Authority data")
	}

	if _, err := ds.Spec.TLSConfig.ClientCertificate.Certificate.GetContent(ctx, r.client); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "invalid Client Certificate data")
	}

	if _, err := ds.Spec.TLSConfig.ClientCertificate.PrivateKey.GetContent(ctx, r.client); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "invalid Client Certificate data")
	}

	tcpList := kamajiv1alpha1.TenantControlPlaneList{}

	if err := r.client.List(ctx, &tcpList); err != nil {
		return reconcile.Result{}, err
	}
	// Updating the status with the list of Tenant Control Plane using the following Data Source
	tcpSets := sets.NewString()
	for _, tcp := range tcpList.Items {
		tcpSets.Insert(getNamespacedName(tcp.GetNamespace(), tcp.GetName()).String())
	}

	ds.Status.UsedBy = tcpSets.List()

	if err := r.client.Status().Update(ctx, &ds); err != nil {
		return reconcile.Result{}, err
	}
	// Triggering the reconciliation of the Tenant Control Plane upon a Secret change
	for _, i := range tcpList.Items {
		tcp := i

		r.TenantControlPlaneTrigger <- event.GenericEvent{Object: &tcp}
	}

	return reconcile.Result{}, nil
}

func (r *DataStore) InjectClient(client client.Client) error {
	r.client = client

	return nil
}

func (r *DataStore) SetupWithManager(mgr controllerruntime.Manager) error {
	return controllerruntime.NewControllerManagedBy(mgr).
		For(&kamajiv1alpha1.DataStore{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetName() == r.ResourceName
		}))).
		Complete(r)
}
