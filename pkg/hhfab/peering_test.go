// Copyright 2024 Hedgehog
// SPDX-License-Identifier: Apache-2.0

package hhfab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	vpcapi "go.githedgehog.com/fabric/api/vpc/v1beta1"
	"go.githedgehog.com/fabric/pkg/hhfctl"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VPCPeeringsSuite struct {
	suite.Suite
	workDir          string
	cacheDir         string
	ctx              context.Context
	ctxCancel        context.CancelFunc
	kube             client.Client
	wipeBetweenTests bool
	opts             SetupVPCsOpts
	tcOpts           TestConnectivityOpts
}

func (suite *VPCPeeringsSuite) SetupSuite() {
	var err error
	err = getEnvVars(&suite.workDir, &suite.cacheDir)
	assert.Nil(suite.T(), err)
	suite.ctx, suite.ctxCancel = context.WithTimeout(context.Background(), 10*time.Minute)
	suite.kube, err = GetKubeClient(suite.ctx, suite.workDir)
	assert.Nil(suite.T(), err)
	suite.wipeBetweenTests = true
	suite.opts = SetupVPCsOpts{
		WaitSwitchesReady: true,
		ForceCleanup:      false,
		ServersPerSubnet:  1,
		SubnetsPerVPC:     1,
		VLANNamespace:     "default",
		IPv4Namespace:     "default",
	}
	suite.tcOpts = TestConnectivityOpts{
		WaitSwitchesReady: true,
	}
}

func appendVpcPeeringSpec(vpcPeerings map[string]*vpcapi.VPCPeeringSpec, index1, index2 int, remote string, vpc1Subnets, vpc2Subnets []string) {
	vpc1 := fmt.Sprintf("vpc-%02d", index1)
	vpc2 := fmt.Sprintf("vpc-%02d", index2)
	entryName := fmt.Sprintf("%s--%s", vpc1, vpc2)
	vpc1SP := vpcapi.VPCPeer{}
	vpc1SP.Subnets = vpc1Subnets
	vpc2SP := vpcapi.VPCPeer{}
	vpc2SP.Subnets = vpc2Subnets
	vpcPeerings[entryName] = &vpcapi.VPCPeeringSpec{
		Remote: remote,
		Permit: []map[string]vpcapi.VPCPeer{
			{
				vpc1: vpc1SP,
				vpc2: vpc2SP,
			},
		},
	}
}

func appendExtPeeringSpec(extPeerings map[string]*vpcapi.ExternalPeeringSpec, vpcIndex int, ext string, subnets []string, prefixes []string) {
	entryName := fmt.Sprintf("vpc-%02d--%s", vpcIndex, ext)
	vpc := fmt.Sprintf("vpc-%02d", vpcIndex)
	prefixesSpec := make([]vpcapi.ExternalPeeringSpecPrefix, len(prefixes))
	for i, prefix := range prefixes {
		prefixesSpec[i] = vpcapi.ExternalPeeringSpecPrefix{
			Prefix: prefix,
		}
	}
	extPeerings[entryName] = &vpcapi.ExternalPeeringSpec{
		Permit: vpcapi.ExternalPeeringSpecPermit{
			VPC: vpcapi.ExternalPeeringSpecVPC{
				Name:    vpc,
				Subnets: subnets,
			},
			External: vpcapi.ExternalPeeringSpecExternal{
				Name:     ext,
				Prefixes: prefixesSpec,
			},
		},
	}
}

func getEnvVars(workDir, cacheDir *string) error {
	*workDir = os.Getenv("HHFAB_WORK_DIR")
	*cacheDir = os.Getenv("HHFAB_CACHE_DIR")

	if *workDir == "" || *cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if *workDir == "" {
			*workDir = filepath.Join(home, "hhfab")
		}
		if *cacheDir == "" {
			*cacheDir = filepath.Join(home, ".hhfab-cache")
		}
	}

	return nil
}

func (suite *VPCPeeringsSuite) TestVPCPeeringsStarter() {
	defer suite.ctxCancel()

	// FIXME: Remove me once the gnmi issue is fixed
	if suite.wipeBetweenTests {
		if err := hhfctl.VPCWipe(suite.ctx); err != nil {
			suite.T().Fatalf("VPCWipe: %v", err)
		}
	}

	if err := DoVLABSetupVPCs(suite.ctx, suite.workDir, suite.cacheDir, suite.opts); err != nil {
		suite.T().Fatalf("DoVLABSetupVPCs: %v", err)
	}
	extName := "default--5835"
	emptyStr := []string{}

	// 1+2 1+3 3+5 2+4 4+6 5+6 5~default--5835:s=subnet-01 6~default--5835:s=subnet-01  1~default--5835:s=subnet-01  2~default--5835:s=subnet-01
	vpcPeerings := make(map[string]*vpcapi.VPCPeeringSpec, 6)
	appendVpcPeeringSpec(vpcPeerings, 1, 2, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 1, 3, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 3, 5, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 2, 4, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 4, 6, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 5, 6, "", emptyStr, emptyStr)

	externalPeerings := make(map[string]*vpcapi.ExternalPeeringSpec, 4)
	appendExtPeeringSpec(externalPeerings, 5, extName, []string{"subnet-01"}, emptyStr)
	appendExtPeeringSpec(externalPeerings, 6, extName, []string{"subnet-01"}, emptyStr)
	appendExtPeeringSpec(externalPeerings, 1, extName, []string{"subnet-01"}, emptyStr)
	appendExtPeeringSpec(externalPeerings, 2, extName, []string{"subnet-01"}, emptyStr)

	if err := DoSetupPeerings(suite.ctx, suite.kube, vpcPeerings, externalPeerings, true); err != nil {
		suite.T().Fatalf("DoSetupPeerings: %v", err)
	}
	if err := DoVLABTestConnectivity(suite.ctx, suite.workDir, suite.cacheDir, suite.tcOpts); err != nil {
		suite.T().Fatalf("DoVLABTestConnectivity: %v", err)
	}
}

func (suite *VPCPeeringsSuite) TestVPCPeeringsFullMeshPlusExternal() {
	defer suite.ctxCancel()

	// FIXME: Remove me once the gnmi issue is fixed
	if suite.wipeBetweenTests {
		if err := hhfctl.VPCWipe(suite.ctx); err != nil {
			suite.T().Fatalf("VPCWipe: %v", err)
		}
	}

	if err := DoVLABSetupVPCs(suite.ctx, suite.workDir, suite.cacheDir, suite.opts); err != nil {
		suite.T().Fatalf("DoVLABSetupVPCs: %v", err)
	}
	extName := "default--5835"
	emptyStr := []string{}

	// 1+2 5+6 1+3 1+4 1+5 1+6 2+6 2+4 2+3 2+5 3+4 3+5 3+6 4+5 4+6 1~default--5835:s=subnet-01 2~default--5835:s=subnet-01
	vpcPeerings := make(map[string]*vpcapi.VPCPeeringSpec, 15)
	appendVpcPeeringSpec(vpcPeerings, 1, 2, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 1, 3, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 1, 4, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 1, 5, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 1, 6, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 2, 3, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 2, 4, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 2, 5, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 2, 6, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 3, 4, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 3, 5, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 3, 6, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 4, 5, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 4, 6, "", emptyStr, emptyStr)
	appendVpcPeeringSpec(vpcPeerings, 5, 6, "", emptyStr, emptyStr)

	externalPeerings := make(map[string]*vpcapi.ExternalPeeringSpec, 2)
	appendExtPeeringSpec(externalPeerings, 1, extName, []string{"subnet-01"}, emptyStr)
	appendExtPeeringSpec(externalPeerings, 2, extName, []string{"subnet-01"}, emptyStr)

	if err := DoSetupPeerings(suite.ctx, suite.kube, vpcPeerings, externalPeerings, true); err != nil {
		suite.T().Fatalf("DoSetupPeerings: %v", err)
	}
	if err := DoVLABTestConnectivity(suite.ctx, suite.workDir, suite.cacheDir, suite.tcOpts); err != nil {
		suite.T().Fatalf("DoVLABTestConnectivity: %v", err)
	}
}
