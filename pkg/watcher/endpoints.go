package watcher

import (
	"fmt"
	"strconv"

	v1 "k8s.io/api/core/v1"
)

type EndpointProbe struct {
	URL       string
	Interval  int
	Timeout   int
	Window    int
	Threshold int
}

type Endpoint struct {
	Name  string
	Host  string
	Port  string
	Probe *EndpointProbe
}

type EndpointList []Endpoint

// Checks if the endpoint from receiver and parameter is the same
func (l EndpointList) EqualsEndpoints(ep v1.EndpointSubset) bool {
	// if the length of address is different, it cannot be the same endpoint
	if len(l) != len(ep.Addresses) {
		return false
	}

	// Declare a map var with string key and bool value
	matchingAddresses := map[string]bool{}
	// iterate EndpointList l
	for i := range l {
		// Not sure why it cannot be i.Host
		// Create a hashmap for Hosts
		matchingAddresses[l[i].Host] = true
	}

	// iterrate endpoints from k8s API (Not sure)
	for i := range ep.Addresses {
		// h is the IP of endpoint
		h := ep.Addresses[i].IP
		// find if that IP(Host) from the hashmap created in the previous for loop. This will show if l and ep shares the same host or not
		_, ok := matchingAddresses[h]
		if !ok {
			return false
		}
	}

	return true
}

func (l EndpointList) Contains(b *Endpoint) bool {
	if b == nil {
		return false
	}

	for i := range l {
		// if the host and port of l and b are the same
		if l[i].Host == b.Host && l[i].Port == b.Port {
			return true
		}
	}

	return false
}

func EndpointListFromSubset(ep v1.EndpointSubset, portName string) (EndpointList, error) {
	var port int32

	// make an Endpotlist list with the size of ep
	l := make(EndpointList, len(ep.Addresses))

	for i := range ep.Ports {
		// find a port based on the portName parameter
		if ep.Ports[i].Name == portName {
			port = ep.Ports[i].Port
		}
	}

	if port == 0 {
		return nil, fmt.Errorf("port '%s' not found in endpoint list", portName)
	}

	for i := range ep.Addresses {
		a := &ep.Addresses[i]

		// fill up name to the list l
		if a.TargetRef != nil {
			l[i].Name = a.TargetRef.Name
		}

		// fill up the IP and port to the list l
		l[i].Host = a.IP
		l[i].Port = strconv.Itoa(int(port))
	}

	return l, nil
}
