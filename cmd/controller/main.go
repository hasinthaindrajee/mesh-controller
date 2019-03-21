/*
 * Copyright (c) 2018 WSO2 Inc. (http:www.wso2.org) All Rights Reserved.
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

package main

import (
	"flag"
	"time"

	"github.com/cellery-io/mesh-controller/pkg/version"

	"k8s.io/client-go/tools/cache"

	"github.com/golang/glog"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cellery-io/mesh-controller/pkg/apis/mesh"
	meshclientset "github.com/cellery-io/mesh-controller/pkg/client/clientset/versioned"
	meshinformers "github.com/cellery-io/mesh-controller/pkg/client/informers/externalversions"
	"github.com/cellery-io/mesh-controller/pkg/controller/cell"
	"github.com/cellery-io/mesh-controller/pkg/controller/gateway"
	"github.com/cellery-io/mesh-controller/pkg/controller/service"
	"github.com/cellery-io/mesh-controller/pkg/controller/sts"
	"github.com/cellery-io/mesh-controller/pkg/signals"
)

const (
	threadsPerController = 2
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	flag.Parse()

	stopCh := signals.SetupSignalHandler()

	glog.Infoln(version.String())

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		glog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		glog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	meshClient, err := meshclientset.NewForConfig(cfg)
	if err != nil {
		glog.Fatalf("Error building mesh clientset: %s", err.Error())
	}

	// Create informer factories
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	meshInformerFactory := meshinformers.NewSharedInformerFactory(meshClient, time.Second*30)
	meshSystemInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		kubeClient,
		time.Second*30,
		kubeinformers.WithNamespace(mesh.SystemNamespace),
	)

	// Create K8s informers
	k8sServiceInformer := kubeInformerFactory.Core().V1().Services()
	configMapInformer := kubeInformerFactory.Core().V1().ConfigMaps()
	secretInformer := kubeInformerFactory.Core().V1().Secrets()
	deploymentInformer := kubeInformerFactory.Apps().V1().Deployments()
	hpaInformer := kubeInformerFactory.Autoscaling().V2beta1().HorizontalPodAutoscalers()
	networkPolicyInformer := kubeInformerFactory.Networking().V1().NetworkPolicies()
	clusterIngressInformer := kubeInformerFactory.Extensions().V1beta1().Ingresses()

	// Create Mesh informers
	cellInformer := meshInformerFactory.Mesh().V1alpha1().Cells()
	gatewayInformer := meshInformerFactory.Mesh().V1alpha1().Gateways()
	tokenServiceInformer := meshInformerFactory.Mesh().V1alpha1().TokenServices()
	serviceInformer := meshInformerFactory.Mesh().V1alpha1().Services()
	envoyFilterInformer := meshInformerFactory.Networking().V1alpha3().EnvoyFilters()
	istioGatewayInformer := meshInformerFactory.Networking().V1alpha3().Gateways()
	istioDRInformer := meshInformerFactory.Networking().V1alpha3().DestinationRules()
	istioVSInformer := meshInformerFactory.Networking().V1alpha3().VirtualServices()

	// Create Mesh system informers
	systemConfigMapInformer := meshSystemInformerFactory.Core().V1().ConfigMaps()

	// Create crd controllers
	cellController := cell.NewController(
		kubeClient,
		meshClient,
		cellInformer,
		gatewayInformer,
		tokenServiceInformer,
		serviceInformer,
		networkPolicyInformer,
		secretInformer,
		envoyFilterInformer,
	)
	gatewayController := gateway.NewController(
		kubeClient,
		meshClient,
		systemConfigMapInformer,
		deploymentInformer,
		k8sServiceInformer,
		clusterIngressInformer,
		istioGatewayInformer,
		istioDRInformer,
		istioVSInformer,
		envoyFilterInformer,
		configMapInformer,
		gatewayInformer,
	)
	tokenServiceController := sts.NewController(
		kubeClient,
		meshClient,
		systemConfigMapInformer,
		deploymentInformer,
		k8sServiceInformer,
		envoyFilterInformer,
		configMapInformer,
		tokenServiceInformer,
	)
	serviceController := service.NewController(
		kubeClient,
		meshClient,
		deploymentInformer,
		hpaInformer,
		k8sServiceInformer,
		serviceInformer,
	)

	// Start informers
	go kubeInformerFactory.Start(stopCh)
	go meshInformerFactory.Start(stopCh)
	go meshSystemInformerFactory.Start(stopCh)

	// Wait for cache sync
	glog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		// Sync K8s informers
		k8sServiceInformer.Informer().HasSynced,
		deploymentInformer.Informer().HasSynced,
		configMapInformer.Informer().HasSynced,
		secretInformer.Informer().HasSynced,
		networkPolicyInformer.Informer().HasSynced,
		systemConfigMapInformer.Informer().HasSynced,
		clusterIngressInformer.Informer().HasSynced,
		// Sync mesh informers
		cellInformer.Informer().HasSynced,
		gatewayInformer.Informer().HasSynced,
		tokenServiceInformer.Informer().HasSynced,
		serviceInformer.Informer().HasSynced,
		envoyFilterInformer.Informer().HasSynced,
		istioGatewayInformer.Informer().HasSynced,
		istioDRInformer.Informer().HasSynced,
		istioVSInformer.Informer().HasSynced,
	); !ok {
		glog.Fatal("failed to wait for caches to sync")
	}

	//Start controllers
	go cellController.Run(threadsPerController, stopCh)
	go gatewayController.Run(threadsPerController, stopCh)
	go tokenServiceController.Run(threadsPerController, stopCh)
	go serviceController.Run(threadsPerController, stopCh)

	// Prevent exiting the main process
	<-stopCh
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
