package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// ResourceGns3Qemu defines a new Terraform resource for creating a QEMU VM instance in GNS3.
func resourceGns3Qemu() *schema.Resource {
	return &schema.Resource{
		Create: resourceGns3QemuCreate,
		Read:   resourceGns3QemuRead,
		Update: resourceGns3QemuUpdate,
		Delete: resourceGns3QemuDelete,
		Importer: &schema.ResourceImporter{
			StateContext: resourceQemuImporter, // use custom importer
		},
		Schema: map[string]*schema.Schema{
			"project_id": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The UUID of the GNS3 project",
			},
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the QEMU VM instance",
			},
			"adapter_type": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "e1000",
				Description: "QEMU adapter type",
			},
			"adapters": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     1,
				Description: "Number of network adapters",
			},
			"bios_image": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Path to the QEMU BIOS image",
			},
			"cdrom_image": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Path to the QEMU CDROM image",
			},
			"console": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Console TCP port",
			},
			"console_type": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "telnet",
				Description: "Console type (telnet, vnc, spice, etc.)",
			},
			"cpus": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     1,
				Description: "Number of vCPUs",
			},
			"ram": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     256,
				Description: "Amount of RAM in MB",
			},
			"mac_address": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Explicit MAC address to assign to the VM's primary network interface",
			},
			"options": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Additional QEMU options (e.g. -smbios to set serial number)",
			},
			"start_vm": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "If true, start the QEMU VM instance after creation",
			},
			"platform": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Platform architecture for QEMU node (e.g. x86_64, aarch64). Required to determine QEMU binary.",
			},
			"hda_disk_image": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Path to the HDA (bootable) disk image file for the QEMU node",
			},
			"x": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  0,
			},
			"y": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  0,
			},
			"symbol": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "ID of the graphical symbol representing the node on the GNS3 canvas.",
				Default:     ":/symbols/classic/computer.svg",
			},
		},
	}
}

func resourceGns3QemuCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*ProviderConfig)
	projectID := d.Get("project_id").(string)

	name := d.Get("name").(string)
	adapterType := d.Get("adapter_type").(string)
	adapters := d.Get("adapters").(int)
	biosImage := d.Get("bios_image").(string)
	cdromImage, _ := d.GetOk("cdrom_image")
	consoleVal, consoleOk := d.GetOk("console")
	consoleType := d.Get("console_type").(string)
	cpus := d.Get("cpus").(int)
	ram := d.Get("ram").(int)
	platform := d.Get("platform").(string)
	x := d.Get("x").(int)
	y := d.Get("y").(int)
	symbol := d.Get("symbol").(string)

	properties := map[string]interface{}{
		"adapter_type": adapterType,
		"adapters":     adapters,
		"bios_image":   biosImage,
		"cdrom_image":  "",
		"console_type": consoleType,
		"ram":          ram,
		"cpus":         cpus,
		"platform":     platform,
	}

	if cdromImage != nil {
		properties["cdrom_image"] = cdromImage.(string)
	}
	if consoleOk {
		properties["console"] = consoleVal.(int)
	}
	if v, ok := d.GetOk("mac_address"); ok {
		properties["mac_address"] = v.(string)
	}
	if v, ok := d.GetOk("options"); ok {
		properties["options"] = v.(string)
	}
	if v, ok := d.GetOk("hda_disk_image"); ok {
		properties["hda_disk_image"] = v.(string)
		properties["hda_disk_interface"] = "virtio"
	}

	// Controller-level API
	payload := map[string]interface{}{
		"name":       name,
		"node_type":  "qemu",
		"compute_id": "local", // adjust if needed
		"x":          x,
		"y":          y,
		"symbol":     symbol,
		"properties": properties,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal QEMU controller payload: %s", err)
	}

	url := fmt.Sprintf("%s/v2/projects/%s/nodes", config.Host, projectID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create QEMU node via controller: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("controller rejected QEMU node creation, status: %d, response: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode controller response: %s", err)
	}

	nodeID, ok := result["node_id"].(string)
	if !ok || nodeID == "" {
		return fmt.Errorf("node_id not returned by controller")
	}
	d.SetId(nodeID)

	// Start VM if requested
	if d.Get("start_vm").(bool) {
		startURL := fmt.Sprintf("%s/v2/projects/%s/nodes/%s/start", config.Host, projectID, nodeID)
		req, err := http.NewRequest("POST", startURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create start request: %s", err)
		}
		startResp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to start QEMU node: %s", err)
		}
		defer startResp.Body.Close()
		if startResp.StatusCode != http.StatusOK {
			body, _ := ioutil.ReadAll(startResp.Body)
			return fmt.Errorf("failed to start node, status: %d, response: %s", startResp.StatusCode, string(body))
		}
	}

	return resourceGns3QemuRead(d, meta)
}

func resourceGns3QemuRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*ProviderConfig)
	host := config.Host
	projectID := d.Get("project_id").(string)
	nodeID := d.Id()

	// Use the controller's project/node endpoint, not the compute API path
	apiURL := fmt.Sprintf("%s/v2/projects/%s/nodes/%s", host, projectID, nodeID)
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Errorf("failed to read QEMU node: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		d.SetId("")
		return nil
	} else if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to read QEMU node, status: %d, response: %s", resp.StatusCode, body)
	}

	var node map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return fmt.Errorf("failed to decode node details: %s", err)
	}

	d.Set("name", node["name"])
	if xVar, ok := node["x"].(float64); ok {
		d.Set("x", int(xVar))
	}
	if yVar, ok := node["y"].(float64); ok {
		d.Set("y", int(yVar))
	}
	if props, ok := node["properties"].(map[string]interface{}); ok {
		if v, ok := props["adapter_type"].(string); ok && v != "" {
			d.Set("adapter_type", v)
		}
	}
	if props, ok := node["properties"].(map[string]interface{}); ok {
		if v, ok := props["adapters"].(float64); ok {
			d.Set("adapters", v)
		}
	}
	if props, ok := node["properties"].(map[string]interface{}); ok {
		if v, ok := props["bios_image"].(string); ok {
			d.Set("bios_image", v)
		}
	}
	if props, ok := node["properties"].(map[string]interface{}); ok {
		if v, ok := props["cdrom_image"].(string); ok {
			d.Set("cdrom_image", v)
		}
	}
	if props, ok := node["properties"].(map[string]interface{}); ok {
		if v, ok := props["cpus"].(float64); ok && v != 0 {
			d.Set("cpus", v)
		}
	
	}
	return nil
}

func resourceGns3QemuUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*ProviderConfig)
	host := config.Host
	projectID := d.Get("project_id").(string)
	nodeID := d.Id()

	updateData := map[string]interface{}{}
	properties := map[string]interface{}{}

	// Top-level fields
	if d.HasChange("name") {
		updateData["name"] = d.Get("name").(string)
	}

	if d.HasChange("x") {
		updateData["x"] = d.Get("x").(int)
	}

	if d.HasChange("y") {
		updateData["y"] = d.Get("y").(int)
	}

	if d.HasChange("symbol") {
		updateData["symbol"] = d.Get("symbol").(string)
	}

	// QEMU-specific properties (inside "properties")
	if d.HasChange("adapter_type") {
		properties["adapter_type"] = d.Get("adapter_type").(string)
	}

	if d.HasChange("adapters") {
		properties["adapters"] = d.Get("adapters").(int)
	}

	if d.HasChange("bios_image") {
		properties["bios_image"] = d.Get("bios_image").(string)
	}

	if d.HasChange("cdrom_image") {
		properties["cdrom_image"] = d.Get("cdrom_image").(string)
	}

	if d.HasChange("cpus") {
		properties["cpus"] = d.Get("cpus").(int)
	}

	if d.HasChange("ram") {
		properties["ram"] = d.Get("ram").(int)
	}

	if d.HasChange("console_type") {
		properties["console_type"] = d.Get("console_type").(string)
	}

	if d.HasChange("platform") {
		properties["platform"] = d.Get("platform").(string)
	}

	if d.HasChange("hda_disk_image") {
		properties["hda_disk_image"] = d.Get("hda_disk_image").(string)
	}

	if d.HasChange("mac_address") {
		properties["mac_address"] = d.Get("mac_address").(string)
	}

	if d.HasChange("options") {
		properties["options"] = d.Get("options").(string)
	}

	// Only include "properties" if we actually changed something in it
	if len(properties) > 0 {
		updateData["properties"] = properties
	}

	// Nothing to update
	if len(updateData) == 0 {
		return nil
	}

	data, err := json.Marshal(updateData)
	if err != nil {
		return fmt.Errorf("failed to marshal update data: %s", err)
	}

	url := fmt.Sprintf("%s/v2/projects/%s/nodes/%s", host, projectID, nodeID)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create update request: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update QEMU node: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to update QEMU node, status code: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	return resourceGns3QemuRead(d, meta)
}
func resourceGns3QemuDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*ProviderConfig)
	projectID := d.Get("project_id").(string)
	nodeID := d.Id()

	// Use the controller's project/node endpoint for delete as well
	apiURL := fmt.Sprintf("%s/v2/projects/%s/nodes/%s", config.Host, projectID, nodeID)
	req, err := http.NewRequest("DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create DELETE request: %s", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete QEMU node: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete QEMU node, status: %d, response: %s", resp.StatusCode, body)
	}
	d.SetId("")
	return nil
}

// resourceQemuImporter supports both comma- and slash-separated import IDs:
//
//	<node_id>,<project_id>
//	<project_id>/<node_id>
func resourceQemuImporter(
	ctx context.Context,
	d *schema.ResourceData,
	meta interface{},
) ([]*schema.ResourceData, error) {
	raw := d.Id()
	var nodeID, projectID string

	if strings.Contains(raw, ",") {
		parts := strings.SplitN(raw, ",", 2)
		nodeID, projectID = parts[0], parts[1]
	} else if strings.Contains(raw, "/") {
		parts := strings.SplitN(raw, "/", 2)
		projectID, nodeID = parts[0], parts[1]
	} else {
		return nil, fmt.Errorf(
			"invalid import ID %q: expected <node_id>,<project_id> or <project_id>/<node_id>",
			raw,
		)
	}

	// seed the required attribute:
	if err := d.Set("project_id", projectID); err != nil {
		return nil, err
	}
	// Terraform resource ID must be the node ID:
	d.SetId(nodeID)

	return []*schema.ResourceData{d}, nil
}
