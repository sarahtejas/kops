/*
Copyright 2019 The Kubernetes Authors.

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

package gce

import (
	"context"
	"fmt"
	"net"

	compute "google.golang.org/api/compute/v1"
	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/util/subnet"
	"k8s.io/kops/upup/pkg/fi"
)

// UsesIPAliases checks if the cluster uses IP aliases for network connectivity
func UsesIPAliases(c *kops.Cluster) bool {
	if c.Spec.Networking != nil && c.Spec.Networking.GCE != nil {
		return true
	}
	return false
}

// PerformNetworkAssignments assigns suitable pod and service assignments for GCE,
// in particular for IP alias support.
func PerformNetworkAssignments(c *kops.Cluster, cloudObj fi.Cloud) error {
	ctx := context.TODO()

	if UsesIPAliases(c) {
		return performNetworkAssignmentsIPAliases(ctx, c, cloudObj)
	}
	return nil
}

func performNetworkAssignmentsIPAliases(ctx context.Context, c *kops.Cluster, cloudObj fi.Cloud) error {
	if len(c.Spec.Subnets) != 1 {
		return fmt.Errorf("expected exactly one subnet with GCE IP Aliases")
	}
	nodeSubnet := &c.Spec.Subnets[0]

	if c.Spec.PodCIDR != "" && c.Spec.ServiceClusterIPRange != "" && nodeSubnet.CIDR != "" {
		return nil
	}

	networkName := c.Spec.NetworkID
	if networkName == "" {
		networkName = "default"
	}

	cloud := cloudObj.(GCECloud)

	regions, err := cloud.Compute().Regions().List(ctx, cloud.Project())
	if err != nil {
		return fmt.Errorf("error listing Regions: %v", err)
	}

	network, err := cloud.Compute().Networks().Get(cloud.Project(), networkName)
	if err != nil {
		return fmt.Errorf("error fetching network name %q: %v", networkName, err)
	}

	subnetURLs := make(map[string]bool)
	for _, subnet := range network.Subnetworks {
		subnetURLs[subnet] = true
	}

	klog.Infof("scanning regions for subnetwork CIDR allocations")

	var subnets []*compute.Subnetwork
	for _, r := range regions {
		l, err := cloud.Compute().Subnetworks().List(ctx, cloud.Project(), r.Name)
		if err != nil {
			return fmt.Errorf("error listing Subnetworks: %v", err)
		}
		subnets = append(subnets, l...)
	}

	var used subnet.CIDRMap
	for _, subnet := range subnets {
		if !subnetURLs[subnet.SelfLink] {
			continue
		}
		if err := used.MarkInUse(subnet.IpCidrRange); err != nil {
			return err
		}

		for _, s := range subnet.SecondaryIpRanges {
			if err := used.MarkInUse(s.IpCidrRange); err != nil {
				return err
			}
		}
	}

	// CIDRs should be in the RFC1918 range, but otherwise we have no constraints
	networkCIDR := "10.0.0.0/8"

	podCIDR, err := used.Allocate(networkCIDR, net.CIDRMask(14, 32))
	if err != nil {
		return err
	}

	serviceCIDR, err := used.Allocate(networkCIDR, net.CIDRMask(20, 32))
	if err != nil {
		return err
	}

	nodeCIDR, err := used.Allocate(networkCIDR, net.CIDRMask(20, 32))
	if err != nil {
		return err
	}

	klog.Infof("Will use %v for Nodes, %v for Pods and %v for Services", nodeCIDR, podCIDR, serviceCIDR)

	nodeSubnet.CIDR = nodeCIDR.String()
	c.Spec.PodCIDR = podCIDR.String()
	c.Spec.ServiceClusterIPRange = serviceCIDR.String()

	return nil
}
