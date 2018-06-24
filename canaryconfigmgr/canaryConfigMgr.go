/*
Copyright 2016 The Fission Authors.

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

package canaryconfigmgr

import (
	"log"

	k8sCache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/fission/fission/crd"
	"time"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/gengo/testdata/a"
)

type canaryConfigMgr struct {
	fissionClient     *crd.FissionClient
	kubeClient        *kubernetes.Clientset
	canaryConfigStore         k8sCache.Store
	canaryConfigController    k8sCache.Controller
	requestTracker *RequestTracker
	crdClient         *rest.RESTClient
}

func MakeCanaryConfigMgr(fissionClient *crd.FissionClient, kubeClient *kubernetes.Clientset, crdClient *rest.RESTClient) (*canaryConfigMgr) {
	configMgr := &canaryConfigMgr{
		fissionClient: fissionClient,
		kubeClient: kubeClient,
		crdClient: crdClient,
		requestTracker: makeRequestTracker(),

	}

	store, controller := configMgr.initCanaryConfigController()
	configMgr.canaryConfigStore = store
	configMgr.canaryConfigController = controller

	return configMgr
}

func(canaryCfgMgr *canaryConfigMgr) initCanaryConfigController() (k8sCache.Store, k8sCache.Controller) {
	resyncPeriod := 30 * time.Second
	listWatch := k8sCache.NewListWatchFromClient(canaryCfgMgr.crdClient, "canaryconfigs", metav1.NamespaceAll, fields.Everything())
	store, controller := k8sCache.NewInformer(listWatch, &crd.CanaryConfig{}, resyncPeriod,
		k8sCache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				canaryConfig := obj.(*crd.CanaryConfig)
				go canaryCfgMgr.addCanaryConfig(canaryConfig)
			},
			DeleteFunc: func(obj interface{}) {
				canaryConfig := obj.(*crd.CanaryConfig)
				go canaryCfgMgr.deleteCanaryConfig(canaryConfig)
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldConfig := oldObj.(*crd.HTTPTrigger)
				newConfig := newObj.(*crd.HTTPTrigger)
				go canaryCfgMgr.updateCanaryConfig(oldConfig, newConfig)

			},
		})
	return store, controller
}

func(canaryCfgMgr *canaryConfigMgr) addCanaryConfig(canaryConfig *crd.CanaryConfig) {
	ticker := time.NewTicker(canaryConfig.Spec.WeightIncrementDuration)
	quit := make(chan struct{})

	for {
		select {
		case <- ticker.C:
			// every weightIncrementDuration, check if failureThreshold has reached.
			// if yes, rollback.
			// else, increment the weight percentage of funcN and decrement funcN-1 by `weightIncrement`
			canaryCfgMgr.processCanaryConfig(canaryConfig, quit)
		case <- quit:
			ticker.Stop()
			return
		}
	}
}


func(canaryCfgMgr *canaryConfigMgr) processCanaryConfig(canaryConfig *crd.CanaryConfig, quit chan struct{}) {
	requestCounter := canaryCfgMgr.requestTracker.get(&canaryConfig.Spec.Trigger)

	if requestCounter == nil || requestCounter.TotalRequests == 0 {
		// nothing to do yet, we need to measure failure percentage of a few requests before incrementing the
		// weight of functionN
		return
	}

	failurePercent := calculatePercentageFailure(requestCounter)

	if failurePercent > canaryConfig.Spec.FailureThreshold {
		// TODO : Need to decide the behavior or rollback.
		rollback()
		close(quit)
		return
	}

	// time to increment the weight of functionN and decrement the weight of functionN-1 by `weightIncrement`
	t, err := canaryCfgMgr.fissionClient.HTTPTriggers(canaryConfig.Metadata.Namespace).Get(canaryConfig.Spec.Trigger.Name)
	if err != nil {
		// nothing to do, because the trigger object is missing
		return
	}

	functionWeights := t.Spec.FunctionReference.FunctionWeights
	functionWeights[canaryConfig.Spec.FunctionN] += canaryConfig.Spec.WeightIncrement
	functionWeights[canaryConfig.Spec.FunctionNminus1] -= canaryConfig.Spec.WeightIncrement

	// TODO : handle a case where increment has a number > 100, set it to 100. sim'ly for < 0, set to 0

	t.Spec.FunctionReference.FunctionWeights = functionWeights
	_, err = canaryCfgMgr.fissionClient.HTTPTriggers(canaryConfig.Metadata.Namespace).Update(t)
	if err != nil {
		// TODO : Add retries for write of trigger object with increased/decreased weight distribution
		return
	}

	// if write was successful and if the functionN has reached 100% and functionN-1 0%, then quit, our job is done.
	if functionWeights[canaryConfig.Spec.FunctionN] >= 100 {
		close(quit)
	}
}


func calculatePercentageFailure(reqCounter *RequestCounter) int {
	if reqCounter.TotalRequests != 0 {
		return int(reqCounter.FailedRequests / reqCounter.TotalRequests * 100)
	}

	return 0
}