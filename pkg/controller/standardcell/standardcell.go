/*
 * Copyright (c) 2019 WSO2 Inc. (http:www.wso2.org) All Rights Reserved.
 *
 * WSO2 Inc. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http:www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package standardcell

import (
	"fmt"
	"reflect"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"

	"github.com/cellery-io/mesh-controller/pkg/apis/mesh/v1alpha1"
	meshclientset "github.com/cellery-io/mesh-controller/pkg/client/clientset/versioned"
	"github.com/cellery-io/mesh-controller/pkg/controller"
	"github.com/cellery-io/mesh-controller/pkg/controller/standardcell/resources"

	istioinformers "github.com/cellery-io/mesh-controller/pkg/client/informers/externalversions/networking/v1alpha3"
	//appsv1informers "k8s.io/client-go/informers/apps/v1"
	//corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	//corev1informers "k8s.io/client-go/informers/core/v1"
	networkv1informers "k8s.io/client-go/informers/networking/v1"
	networkv1listers "k8s.io/client-go/listers/networking/v1"

	meshinformers "github.com/cellery-io/mesh-controller/pkg/client/informers/externalversions/mesh/v1alpha1"
	listers "github.com/cellery-io/mesh-controller/pkg/client/listers/mesh/v1alpha1"
	istiov1alpha1listers "github.com/cellery-io/mesh-controller/pkg/client/listers/networking/v1alpha3"
)

type standardCellHandler struct {
	kubeClient          kubernetes.Interface
	meshClient          meshclientset.Interface
	networkPilicyLister networkv1listers.NetworkPolicyLister
	standardCellLister  listers.StandardCellLister
	gatewayLister       listers.GatewayLister
	tokenServiceLister  listers.TokenServiceLister
	serviceLister       listers.ServiceLister
	envoyFilterLister   istiov1alpha1listers.EnvoyFilterLister
}

func NewController(
	kubeClient kubernetes.Interface,
	meshClient meshclientset.Interface,
	standardCellInformer meshinformers.StandardCellInformer,
	gatewayInformer meshinformers.GatewayInformer,
	tokenServiceInformer meshinformers.TokenServiceInformer,
	serviceInformer meshinformers.ServiceInformer,
	networkPolicyInformer networkv1informers.NetworkPolicyInformer,
	envoyFilterInformer istioinformers.EnvoyFilterInformer,
) *controller.Controller {
	h := &standardCellHandler{
		kubeClient:          kubeClient,
		meshClient:          meshClient,
		standardCellLister:  standardCellInformer.Lister(),
		serviceLister:       serviceInformer.Lister(),
		gatewayLister:       gatewayInformer.Lister(),
		tokenServiceLister:  tokenServiceInformer.Lister(),
		networkPilicyLister: networkPolicyInformer.Lister(),
		envoyFilterLister:   envoyFilterInformer.Lister(),
	}
	c := controller.New(h, "StandardCell")

	glog.Info("Setting up event handlers")
	standardCellInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.Enqueue,
		UpdateFunc: func(old, new interface{}) {
			glog.Infof("Old %+v\nnew %+v", old, new)
			c.Enqueue(new)
		},
		DeleteFunc: c.Enqueue,
	})
	return c
}

func (h *standardCellHandler) Handle(key string) error {
	glog.Infof("Handle called with %s", key)
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		glog.Errorf("invalid resource key: %s", key)
		return nil
	}
	cellOriginal, err := h.standardCellLister.StandardCells(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("standardCell '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}
	glog.Infof("Found standardCell %+v", cellOriginal)
	cell := cellOriginal.DeepCopy()

	if err = h.handle(cell); err != nil {
		return err
	}

	if _, err = h.updateStatus(cell); err != nil {
		return err
	}
	return nil
}

func (h *standardCellHandler) handle(cell *v1alpha1.StandardCell) error {

	if err := h.handleNetworkPolicy(cell); err != nil {
		return err
	}

	if err := h.handleGateway(cell); err != nil {
		return err
	}

	if len(cell.Spec.GatewayTemplate.Spec.TCPRoutes) == 0 {
		if err := h.handleTokenService(cell); err != nil {
			return err
		}

		if err := h.handleEnvoyFilter(cell); err != nil {
			return err
		}
	}

	if err := h.handleServices(cell); err != nil {
		return err
	}

	h.updateCellStatus(cell)
	return nil
}

func (h *standardCellHandler) handleNetworkPolicy(cell *v1alpha1.StandardCell) error {
	networkPolicy, err := h.networkPilicyLister.NetworkPolicies(cell.Namespace).Get(resources.NetworkPolicyName(cell))
	if errors.IsNotFound(err) {
		networkPolicy, err = h.kubeClient.NetworkingV1().NetworkPolicies(cell.Namespace).Create(resources.CreateNetworkPolicy(cell))
		if err != nil {
			glog.Errorf("Failed to create NetworkPolicy %v", err)
			return err
		}
	} else if err != nil {
		return err
	}
	glog.Infof("NetworkPolicy created %+v", networkPolicy)
	return nil
}

func (h *standardCellHandler) handleGateway(cell *v1alpha1.StandardCell) error {
	gateway, err := h.gatewayLister.Gateways(cell.Namespace).Get(resources.GatewayName(cell))
	if errors.IsNotFound(err) {
		gateway, err = h.meshClient.MeshV1alpha1().Gateways(cell.Namespace).Create(resources.CreateGateway(cell))
		if err != nil {
			glog.Errorf("Failed to create Gateway %v", err)
			return err
		}
	} else if err != nil {
		return err
	}
	glog.Infof("Gateway created %+v", gateway)

	cell.Status.GatewayHostname = gateway.Status.HostName
	cell.Status.GatewayStatus = gateway.Status.Status
	return nil
}

func (h *standardCellHandler) handleTokenService(cell *v1alpha1.StandardCell) error {
	tokenService, err := h.tokenServiceLister.TokenServices(cell.Namespace).Get(resources.TokenServiceName(cell))
	if errors.IsNotFound(err) {
		tokenService, err = h.meshClient.MeshV1alpha1().TokenServices(cell.Namespace).Create(resources.CreateTokenService(cell))
		if err != nil {
			glog.Errorf("Failed to create TokenService %v", err)
			return err
		}
	} else if err != nil {
		return err
	}
	glog.Infof("TokenService created %+v", tokenService)
	return nil
}

func (h *standardCellHandler) handleEnvoyFilter(cell *v1alpha1.StandardCell) error {
	envoyFilter, err := h.envoyFilterLister.EnvoyFilters(cell.Namespace).Get(resources.EnvoyFilterName(cell))
	if errors.IsNotFound(err) {
		envoyFilter, err = h.meshClient.NetworkingV1alpha3().EnvoyFilters(cell.Namespace).Create(resources.CreateEnvoyFilter(cell))
		if err != nil {
			glog.Errorf("Failed to create EnvoyFilter %v", err)
			return err
		}
	} else if err != nil {
		return err
	}
	glog.Infof("EnvoyFilter created %+v", envoyFilter)
	return nil
}

func (h *standardCellHandler) handleServices(cell *v1alpha1.StandardCell) error {
	servicesSpecs := cell.Spec.ServiceTemplates
	cell.Status.ServiceCount = 0
	for _, serviceSpec := range servicesSpecs {
		service, err := h.serviceLister.Services(cell.Namespace).Get(resources.ServiceName(cell, serviceSpec))
		if errors.IsNotFound(err) {
			service, err = h.meshClient.MeshV1alpha1().Services(cell.Namespace).Create(resources.CreateService(cell, serviceSpec))
			if err != nil {
				glog.Errorf("Failed to create Service: %s : %v", serviceSpec.Name, err)
				return err
			}
		} else if err != nil {
			return err
		}
		glog.Infof("Service '%s' created %+v", serviceSpec.Name, service)
		if service.Status.AvailableReplicas > 0 {
			cell.Status.ServiceCount++
		}
	}
	return nil
}

func (h *standardCellHandler) updateStatus(cell *v1alpha1.StandardCell) (*v1alpha1.StandardCell, error) {
	latestCell, err := h.standardCellLister.StandardCells(cell.Namespace).Get(cell.Name)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(latestCell.Status, cell.Status) {
		latestCell.Status = cell.Status

		return h.meshClient.MeshV1alpha1().StandardCells(cell.Namespace).Update(latestCell)
	}
	return cell, nil
}

func (h *standardCellHandler) updateCellStatus(cell *v1alpha1.StandardCell) {
	if cell.Status.GatewayStatus == "Ready" && int(cell.Status.ServiceCount) == len(cell.Spec.ServiceTemplates) {
		cell.Status.Status = "Ready"
	} else {
		cell.Status.Status = "NotReady"
	}
}
