package watcher

import (
	"time"

	"github.com/golang/glog"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
)

// start a go routine with method watch. in the end returns two channels
func (v *EndpointWatcher) Run() (chan *EndpointConfig, chan error) {
	updates := make(chan *EndpointConfig)
	errors := make(chan error)

	go v.watch(updates, errors)

	return updates, errors
}

func (v *EndpointWatcher) watch(updates chan *EndpointConfig, errors chan error) {
	// indefinte for loop
	for {
		// watch k8s endpoint in certain namespace. with certain service name field.
		w, err := v.client.CoreV1().Endpoints(v.namespace).Watch(metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("metadata.name", v.serviceName).String(),
		})

		if err != nil {
			glog.Errorf("error while establishing watch: %s", err.Error())
			glog.Infof("retrying after %s", v.retryBackoff.String())

			time.Sleep(v.retryBackoff)
			continue
		}

		// create a channel, watch the input from w
		c := w.ResultChan()
		for ev := range c {
			// ev.Type shows the status of watch(not clear)
			if ev.Type == watch.Error {
				glog.Warningf("error while watching: %+v", ev.Object)
				continue
			}

			// if nothing has added or modified, skip the loop
			if ev.Type != watch.Added && ev.Type != watch.Modified {
				continue
			}

			endpoint := ev.Object.(*v1.Endpoints)

			// if the length of endpoint is o or if there is no address in the endpoint object,
			// shows the warning message and construct a new endpoint and assing it to v(receiver)
			if len(endpoint.Subsets) == 0 || len(endpoint.Subsets[0].Addresses) == 0 {
				glog.Warningf("service '%s' has no endpoints", v.serviceName)

				v.endpointConfig = NewEndpointConfig()

				continue
			}

			// v is the current state, endpoint would be the previous state of endpoint
			if v.endpointConfig.Endpoints.EqualsEndpoints(endpoint.Subsets[0]) {
				glog.Infof("endpoints did not change")
				continue
			}

			var addresses []v1.EndpointAddress
			for _, a := range endpoint.Subsets[0].Addresses {
				puid := string(a.TargetRef.UID)

				// Get the pod object
				po, err := v.client.CoreV1().Pods(v.namespace).Get(a.TargetRef.Name, metav1.GetOptions{})

				if err != nil {
					glog.Errorf("error while locating endpoint : %s", err.Error())
					continue
				}

				if len(po.Status.Conditions) > 0 && po.Status.Conditions[0].Status != v1.ConditionTrue {
					glog.Infof("skipping endpoint (not healthy): %s", puid)
					continue
				}

				// append endpoint address a to the list addresses
				addresses = append(addresses, a)
			}

			// if there nothing in the addresses list, construct a new endpoint using NewEndpointConfig() function
			if len(addresses) == 0 {
				glog.Warningf("service '%s' has no endpoint that is ready", v.serviceName)
				v.endpointConfig = NewEndpointConfig()
				continue
			}

			// Not sure why assigning addresses back to the endpoint
			endpoint.Subsets[0].Addresses = addresses

			newConfig := NewEndpointConfig()

			// not sure why is it a BackendList, possibly due the the things been done to the endpoint variable in advance
			newBackendList, err := EndpointListFromSubset(endpoint.Subsets[0], v.portName)
			if err != nil {
				glog.Errorf("error while building backend list: %s", err.Error())
				continue
			}

			// Not sure what is this Primary variable is doing
			if v.endpointConfig.Primary != nil && newBackendList.Contains(v.endpointConfig.Primary) {
				newConfig.Primary = v.endpointConfig.Primary
			} else {
				newConfig.Primary = &newBackendList[0]
			}

			newConfig.Endpoints = newBackendList

			v.endpointConfig = newConfig
			updates <- newConfig
		}

		glog.V(5).Info("watch has ended. starting new watch")
	}
}
