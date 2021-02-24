/*
Copyright 2021.

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

package controllers

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	demosv1alpha1 "github.com/sdbrett/operator-demos/operator-intro/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DemoOneReconciler reconciles a DemoOne object
type DemoOneReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=demos.sdbrett.com,resources=demoones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=demos.sdbrett.com,resources=demoones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=demos.sdbrett.com,resources=demoones/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DemoOne object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *DemoOneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	deploymentChanged := false
	log := r.Log.WithValues("demoone", req.NamespacedName)
	demoone := &demosv1alpha1.DemoOne{}

	err := r.Get(ctx, req.NamespacedName, demoone)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Resource not found, ignoring")
			return ctrl.Result{}, err
		}
	}

	cmfound := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: demoone.Name, Namespace: demoone.Namespace}, cmfound)
	if err != nil && errors.IsNotFound(err) {
		cm := r.configMapForDemoOne(demoone)

		log.Info("Creating a new Config map", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		err = r.Create(ctx, cm)

		if err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	cmData := dataForConfigMap(demoone.Spec.Colour)
	if !reflect.DeepEqual(cmfound.Data, cmData) {
		cmfound.Data = cmData
		err = r.Update(ctx, cmfound)
		if err != nil {
			log.Error(err, "Failed to update ConfigMap", "ConfigMap.Namespace", cmfound.Namespace, "Deployment.Name", cmfound.Name)
			return ctrl.Result{}, err
		}
	}

	cmCurrentRevisionVersion := cmfound.ResourceVersion

	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: demoone.Name, Namespace: demoone.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		dep := r.deploymentForDemoOne(demoone, cmCurrentRevisionVersion)

		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := demoone.Spec.Replicas
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		deploymentChanged = true
	}

	if found.Spec.Template.Annotations["cmRevisionVersion"] != cmCurrentRevisionVersion {
		found.Spec.Template.Annotations["cmRevisionVersion"] = cmCurrentRevisionVersion
		deploymentChanged = true
	}

	if deploymentChanged {
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}

		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	// Update the DemoOne status with the pod names
	// List the pods for this demoone's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(demoone.Namespace),
		client.MatchingLabels(labelsForDemoOne(demoone.Name)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods", "DemoOne.Namespace", demoone.Namespace, "DemoOne.Name", demoone.Name)
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	if !reflect.DeepEqual(podNames, demoone.Status.Nodes) {
		demoone.Status.Nodes = podNames
		err := r.Status().Update(ctx, demoone)
		if err != nil {
			log.Error(err, "Failed to update DemoOne status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *DemoOneReconciler) deploymentForDemoOne(m *demosv1alpha1.DemoOne, cmCurrentRevisionVersion string) *appsv1.Deployment {
	ls := labelsForDemoOne(m.Name)
	replicas := m.Spec.Replicas
	annotations := annotationsForDemoOne(cmCurrentRevisionVersion)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ls,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "nginx",
						Name:  "demoone",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
							Name:          "demoone",
						}},
					}},
				},
			},
		},
	}
	// Set DemoOne instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

func (r *DemoOneReconciler) configMapForDemoOne(m *demosv1alpha1.DemoOne) *corev1.ConfigMap {

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Data: dataForConfigMap(m.Spec.Colour),
	}

	// Set DemoOne instance as the owner and controller
	ctrl.SetControllerReference(m, cm, r.Scheme)
	return cm
}

// labelsForDemoOne returns the labels for selecting the resources
// belonging to the given demoone CR name.
func labelsForDemoOne(name string) map[string]string {
	return map[string]string{"app": "demoone", "demoone_cr": name}
}

func annotationsForDemoOne(cmCurrentRevisionVersion string) map[string]string {
	return map[string]string{"cmRevisionVersion": cmCurrentRevisionVersion}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

func dataForConfigMap(colour string) map[string]string {
	index := "<body><h1 style='color:" + colour + ";'>" + colour + "</h1></body>"
	data := make(map[string]string)

	data["index"] = index

	return data
}

// SetupWithManager sets up the controller with the Manager.
func (r *DemoOneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&demosv1alpha1.DemoOne{}).
		Complete(r)
}
