// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"testing"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bundledTemplateSetsFile  = "../ovn_template_sets/openvswitch.bin"
	helmTemplateSetsTemplate = "../../../cloud-operator/templates/ipfix-template-sets.yaml"

	ipfixVersion = 10
	// Open vSwitch exports IPFIX with observation domain ID 0 unless configured otherwise.
	ovsObservationDomainID = 0
)

// ovsTemplateIDs returns the IDs of all the templates that Open vSwitch exports,
// computed like ipfix_get_template_id in ofproto/ofproto-dpif-ipfix.c: one
// template per combination of L2 (Ethernet, VLAN), L3 (unknown, IPv4, IPv6),
// L4 (unknown, TCP, UDP, SCTP, ICMP), tunneling and flow direction. Open
// vSwitch defines no template with a known L4 and an unknown L3.
func ovsTemplateIDs() map[string][]uint16 {
	const (
		numL2, numL3, numL4, numTunnel, numDirection = 2, 3, 5, 2, 2
		minTemplateID                                = 256
	)

	l3Names := []string{"non-IP", "IPv4", "IPv6"}
	l4Names := []string{"other", "TCP", "UDP", "SCTP", "ICMP"}
	ids := make(map[string][]uint16)

	for l2 := range numL2 {
		for l3 := range numL3 {
			for l4 := range numL4 {
				if l3 == 0 && l4 != 0 {
					continue
				}

				for tunnel := range numTunnel {
					for direction := range numDirection {
						id := (((l2*numL3+l3)*numL4+l4)*numTunnel+tunnel)*numDirection + direction
						name := l3Names[l3] + " " + l4Names[l4]
						ids[name] = append(ids[name], uint16(minTemplateID+id))
					}
				}
			}
		}
	}

	return ids
}

// TestBundledIPFIXTemplateSetsCoverAllOVSTemplates checks that the template
// sets preloaded at startup contain every template that Open vSwitch exports.
// Open vSwitch sends its templates as several messages, only every 10 minutes
// by default, so a flow record whose template is not preloaded cannot be
// decoded until then.
func TestBundledIPFIXTemplateSetsCoverAllOVSTemplates(t *testing.T) {
	packet, err := os.ReadFile(bundledTemplateSetsFile)
	require.NoError(t, err)

	// Decode the file like NewTemplateSystem does: as a single IPFIX message.
	templateSystem := netflows.CreateTemplateSystem()

	_, err = netflows.DecodeMessage(bytes.NewBuffer(packet), templateSystem)
	require.NoError(t, err)

	total := 0

	for name, ids := range ovsTemplateIDs() {
		var missing []uint16

		for _, id := range ids {
			total++

			if _, err := templateSystem.GetTemplate(ipfixVersion, ovsObservationDomainID, id); err != nil {
				missing = append(missing, id)
			}
		}

		assert.Empty(t, missing, "%s templates are not preloaded", name)
	}

	assert.Equal(t, 88, total, "Open vSwitch exports 88 templates")
}

// TestHelmChartIPFIXTemplateSetsMatchBundledFile checks that the ConfigMap in
// the Helm chart contains the bundled template sets file.
func TestHelmChartIPFIXTemplateSetsMatchBundledFile(t *testing.T) {
	bundled, err := os.ReadFile(bundledTemplateSetsFile)
	require.NoError(t, err)

	template, err := os.ReadFile(helmTemplateSetsTemplate)
	require.NoError(t, err)

	match := regexp.MustCompile(`(?m)^\s*openvswitch\.bin:\s*(\S+)\s*$`).FindSubmatch(template)
	require.NotNil(t, match, "openvswitch.bin not found in %s", helmTemplateSetsTemplate)

	inChart, err := base64.StdEncoding.DecodeString(string(match[1]))
	require.NoError(t, err)

	assert.True(t, bytes.Equal(bundled, inChart),
		"the ConfigMap's openvswitch.bin differs from %s", bundledTemplateSetsFile)
}
