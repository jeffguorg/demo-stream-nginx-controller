/*


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
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jeffguorg/demo-stream-nginx-controller/api/v1beta1"
	nginxstreamv1beta1 "github.com/jeffguorg/demo-stream-nginx-controller/api/v1beta1"
)

// StreamIngressReconciler reconciles a StreamIngress object
type StreamIngressReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	TCPConfigMapKey client.ObjectKey
	UDPConfigMapKey client.ObjectKey
	ServiceKey      client.ObjectKey
}

// +kubebuilder:rbac:groups=nginx-stream.jeffthecoder.xyz,resources=streamingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nginx-stream.jeffthecoder.xyz,resources=streamingresses/status,verbs=get;update;patch

func (r *StreamIngressReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	logger := r.Log.WithValues("streamingress", req.NamespacedName)

	// fetch stream ingress and validate
	var (
		requestIngress v1beta1.StreamIngress
		ingressList    v1beta1.StreamIngressList
	)
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, &requestIngress); err != nil {
		logger.Error(err, "failed to get stream ingress")
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	if err := r.List(ctx, &ingressList, client.MatchingFields{
		"listen": fmt.Sprintf("%v/%v", requestIngress.Spec.Protocol, requestIngress.Spec.Listen),
	}); err != nil {
		logger.Error(err, "failed to list stream ingress")
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	if len(ingressList.Items) > 1 {
		logger.WithValues("count", len(ingressList.Items)).Info("multiple listen on one port")
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	// check if upstream service exists
	var (
		upstreamService           v1.Service
		upstreamServicePortIsOpen = false
	)
	if err := r.Get(ctx, client.ObjectKey{Namespace: requestIngress.Spec.Upstream.Namespace, Name: requestIngress.Spec.Upstream.Service}, &upstreamService); err != nil {
		logger.WithValues("service", fmt.Sprintf("%v/%v", requestIngress.Spec.Upstream.Namespace, requestIngress.Spec.Upstream.Service)).Info("unable to get service")
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}
	for _, port := range upstreamService.Spec.Ports {
		if port.Port == int32(requestIngress.Spec.Upstream.Port) {
			upstreamServicePortIsOpen = true
		}
	}
	if !upstreamServicePortIsOpen {
		logger.WithValues("upstream-port", requestIngress.Spec.Upstream.Port).Info("upstream service is not open")
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	// fetch service and config map to update
	var (
		service   v1.Service
		configMap v1.ConfigMap

		portIsAlreadyOnService = false
	)
	if err := r.Get(ctx, r.ServiceKey, &service); err != nil {
		logger.Error(err, "failed to get service")
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}
	if requestIngress.Spec.Protocol == string(v1.ProtocolUDP) {
		if err := r.Get(ctx, r.UDPConfigMapKey, &configMap); err != nil {
			logger.Error(err, "failed to list stream ingress")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
	} else {
		if err := r.Get(ctx, r.TCPConfigMapKey, &configMap); err != nil {
			logger.Error(err, "failed to list stream ingress")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
	}

	// check if port is opened and open it if needed
	for idx, port := range service.Spec.Ports {
		if port.Protocol == v1.Protocol(requestIngress.Spec.Protocol) && port.Port == int32(requestIngress.Spec.Listen) {
			service.Spec.Ports[idx].TargetPort = intstr.FromInt(int(requestIngress.Spec.Listen))
			portIsAlreadyOnService = true
		}
	}
	if !portIsAlreadyOnService {
		service.Spec.Ports = append(service.Spec.Ports, v1.ServicePort{
			Protocol:   v1.Protocol(requestIngress.Spec.Protocol),
			Port:       int32(requestIngress.Spec.Listen),
			TargetPort: intstr.FromInt(int(requestIngress.Spec.Listen)),
		})
	}
	if err := r.Update(ctx, &service); err != nil {
		logger.Error(err, "failed to update service")
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	// update configmap
	configMap.Data[strconv.FormatInt(int64(requestIngress.Spec.Listen), 10)] = fmt.Sprintf("%v/%v:%v", requestIngress.Spec.Upstream.Namespace, requestIngress.Spec.Upstream.Service, requestIngress.Spec.Upstream.Port)
	if err := r.Update(ctx, &configMap); err != nil {
		logger.Error(err, "failed to update configMap")
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	return ctrl.Result{}, nil
}

func (r *StreamIngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nginxstreamv1beta1.StreamIngress{}).
		Complete(r)
}
