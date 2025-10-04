package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	validOSNameLinux   = "linux"
	validOSNameWindows = "windows"
	validProtocolTCP   = "TCP"
	validProtocolUDP   = "UDP"
)

var (
	validMemoryUnit = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)
	validContainerName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	validImageDomain = "registry.bigbrother.io"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]
	if err := validatePodYAML(filePath); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func validatePodYAML(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("%s: cannot read file: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		// Try to get line number from error if possible
		return fmt.Errorf("%s: cannot unmarshal YAML: %w", filePath, err)
	}

	if len(root.Content) == 0 {
		return fmt.Errorf("%s: empty YAML document", filePath)
	}

	doc := root.Content[0]
	if doc.Kind != yaml.DocumentNode {
		doc = &root
	}

	if len(doc.Content) == 0 {
		return fmt.Errorf("%s: apiVersion is required", filePath)
	}

	// Expect mapping node at top level
	if doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: invalid YAML structure", filePath)
	}

	mapping := doc.Content[0]
	if len(mapping.Content)%2 != 0 {
		return fmt.Errorf("%s: invalid mapping structure", filePath)
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		valueNode := mapping.Content[i+1]
		fields[keyNode.Value] = valueNode
	}

	// Validate top-level fields
	if err := validateRequiredField(filePath, fields, "apiVersion", mapping); err != nil {
		return err
	}
	if fields["apiVersion"].Value != "v1" {
		return fmt.Errorf("%s:%d apiVersion has unsupported value '%s'", filePath, fields["apiVersion"].Line, fields["apiVersion"].Value)
	}

	if err := validateRequiredField(filePath, fields, "kind", mapping); err != nil {
		return err
	}
	if fields["kind"].Value != "Pod" {
		return fmt.Errorf("%s:%d kind has unsupported value '%s'", filePath, fields["kind"].Line, fields["kind"].Value)
	}

	if err := validateRequiredField(filePath, fields, "metadata", mapping); err != nil {
		return err
	}
	if err := validateObjectMeta(filePath, fields["metadata"]); err != nil {
		return err
	}

	if err := validateRequiredField(filePath, fields, "spec", mapping); err != nil {
		return err
	}
	if err := validatePodSpec(filePath, fields["spec"]); err != nil {
		return err
	}

	return nil
}

func validateRequiredField(filePath string, fields map[string]*yaml.Node, fieldName string, parent *yaml.Node) error {
	if _, exists := fields[fieldName]; !exists {
		return fmt.Errorf("%s: %s is required", filePath, fieldName)
	}
	return nil
}

func validateObjectMeta(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d metadata must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)
	if err := validateRequiredField(filePath, fields, "name", node); err != nil {
		return err
	}
	if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
		return fmt.Errorf("%s:%d name must be string", filePath, fields["name"].Line)
	}

	// namespace is optional
	if namespaceNode, exists := fields["namespace"]; exists {
		if namespaceNode.Kind != yaml.ScalarNode || namespaceNode.Tag != "!!str" {
			return fmt.Errorf("%s:%d namespace must be string", filePath, namespaceNode.Line)
		}
	}

	// labels is optional
	if labelsNode, exists := fields["labels"]; exists {
		if labelsNode.Kind != yaml.MappingNode {
			return fmt.Errorf("%s:%d labels must be object", filePath, labelsNode.Line)
		}
		// Validate that all label values are strings
		for i := 0; i < len(labelsNode.Content); i += 2 {
			valueNode := labelsNode.Content[i+1]
			if valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!str" {
				return fmt.Errorf("%s:%d labels values must be strings", filePath, valueNode.Line)
			}
		}
	}

	return nil
}

func validatePodSpec(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d spec must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)

	// os is optional
	if osNode, exists := fields["os"]; exists {
		if err := validatePodOS(filePath, osNode); err != nil {
			return err
		}
	}

	if err := validateRequiredField(filePath, fields, "containers", node); err != nil {
		return err
	}
	if err := validateContainers(filePath, fields["containers"]); err != nil {
		return err
	}

	return nil
}

func validatePodOS(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d os must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)
	if err := validateRequiredField(filePath, fields, "name", node); err != nil {
		return err
	}
	if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
		return fmt.Errorf("%s:%d name must be string", filePath, fields["name"].Line)
	}

	osName := fields["name"].Value
	if osName != validOSNameLinux && osName != validOSNameWindows {
		return fmt.Errorf("%s:%d os has unsupported value '%s'", filePath, fields["name"].Line, osName)
	}

	return nil
}

func validateContainers(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s:%d containers must be array", filePath, node.Line)
	}

	containerNames := make(map[string]bool)
	for idx, containerNode := range node.Content {
		if containerNode.Kind != yaml.MappingNode {
			return fmt.Errorf("%s:%d containers[%d] must be object", filePath, containerNode.Line, idx)
		}
		if err := validateContainer(filePath, containerNode, containerNames); err != nil {
			return err
		}
	}

	return nil
}

func validateContainer(filePath string, node *yaml.Node, existingNames map[string]bool) error {
	fields := extractMappingFields(node)

	if err := validateRequiredField(filePath, fields, "name", node); err != nil {
		return err
	}
	if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
		return fmt.Errorf("%s:%d name must be string", filePath, fields["name"].Line)
	}

	name := fields["name"].Value
	if !validContainerName.MatchString(name) {
		return fmt.Errorf("%s:%d name has invalid format '%s'", filePath, fields["name"].Line, name)
	}
	if existingNames[name] {
		return fmt.Errorf("%s:%d name must be unique in pod", filePath, fields["name"].Line)
	}
	existingNames[name] = true

	if err := validateRequiredField(filePath, fields, "image", node); err != nil {
		return err
	}
	if fields["image"].Kind != yaml.ScalarNode || fields["image"].Tag != "!!str" {
		return fmt.Errorf("%s:%d image must be string", filePath, fields["image"].Line)
	}

	image := fields["image"].Value
	if !strings.HasPrefix(image, validImageDomain+"/") {
		return fmt.Errorf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image)
	}
	parts := strings.Split(image, "/")
	if len(parts) < 3 {
		return fmt.Errorf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image)
	}
	tagPart := parts[len(parts)-1]
	if !strings.Contains(tagPart, ":") {
		return fmt.Errorf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image)
	}

	// ports is optional
	if portsNode, exists := fields["ports"]; exists {
		if err := validateContainerPorts(filePath, portsNode); err != nil {
			return err
		}
	}

	// readinessProbe is optional
	if readinessProbeNode, exists := fields["readinessProbe"]; exists {
		if err := validateProbe(filePath, readinessProbeNode); err != nil {
			return err
		}
	}

	// livenessProbe is optional
	if livenessProbeNode, exists := fields["livenessProbe"]; exists {
		if err := validateProbe(filePath, livenessProbeNode); err != nil {
			return err
		}
	}

	if err := validateRequiredField(filePath, fields, "resources", node); err != nil {
		return err
	}
	if err := validateResourceRequirements(filePath, fields["resources"]); err != nil {
		return err
	}

	return nil
}

func validateContainerPorts(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s:%d ports must be array", filePath, node.Line)
	}

	for idx, portNode := range node.Content {
		if portNode.Kind != yaml.MappingNode {
			return fmt.Errorf("%s:%d ports[%d] must be object", filePath, portNode.Line, idx)
		}
		if err := validateContainerPort(filePath, portNode); err != nil {
			return err
		}
	}

	return nil
}

func validateContainerPort(filePath string, node *yaml.Node) error {
	fields := extractMappingFields(node)

	if err := validateRequiredField(filePath, fields, "containerPort", node); err != nil {
		return err
	}
	if fields["containerPort"].Kind != yaml.ScalarNode {
		return fmt.Errorf("%s:%d containerPort must be int", filePath, fields["containerPort"].Line)
	}

	portStr := fields["containerPort"].Value
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%s:%d containerPort must be int", filePath, fields["containerPort"].Line)
	}
	if port <= 0 || port >= 65536 {
		return fmt.Errorf("%s:%d containerPort value out of range", filePath, fields["containerPort"].Line)
	}

	// protocol is optional
	if protocolNode, exists := fields["protocol"]; exists {
		if protocolNode.Kind != yaml.ScalarNode || protocolNode.Tag != "!!str" {
			return fmt.Errorf("%s:%d protocol must be string", filePath, protocolNode.Line)
		}
		protocol := protocolNode.Value
		if protocol != validProtocolTCP && protocol != validProtocolUDP {
			return fmt.Errorf("%s:%d protocol has unsupported value '%s'", filePath, protocolNode.Line, protocol)
		}
	}

	return nil
}

func validateProbe(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d probe must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)
	if err := validateRequiredField(filePath, fields, "httpGet", node); err != nil {
		return err
	}
	if err := validateHTTPGetAction(filePath, fields["httpGet"]); err != nil {
		return err
	}

	return nil
}

func validateHTTPGetAction(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d httpGet must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)

	if err := validateRequiredField(filePath, fields, "path", node); err != nil {
		return err
	}
	if fields["path"].Kind != yaml.ScalarNode || fields["path"].Tag != "!!str" {
		return fmt.Errorf("%s:%d path must be string", filePath, fields["path"].Line)
	}
	path := fields["path"].Value
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%s:%d path must be absolute", filePath, fields["path"].Line)
	}

	if err := validateRequiredField(filePath, fields, "port", node); err != nil {
		return err
	}
	if fields["port"].Kind != yaml.ScalarNode {
		return fmt.Errorf("%s:%d port must be int", filePath, fields["port"].Line)
	}

	portStr := fields["port"].Value
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%s:%d port must be int", filePath, fields["port"].Line)
	}
	if port <= 0 || port >= 65536 {
		return fmt.Errorf("%s:%d port value out of range", filePath, fields["port"].Line)
	}

	return nil
}

func validateResourceRequirements(filePath string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d resources must be object", filePath, node.Line)
	}

	fields := extractMappingFields(node)

	// requests is optional
	if requestsNode, exists := fields["requests"]; exists {
		if err := validateResourceList(filePath, requestsNode, "requests"); err != nil {
			return err
		}
	}

	// limits is optional
	if limitsNode, exists := fields["limits"]; exists {
		if err := validateResourceList(filePath, limitsNode, "limits"); err != nil {
			return err
		}
	}

	return nil
}

func validateResourceList(filePath string, node *yaml.Node, fieldName string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d %s must be object", filePath, node.Line, fieldName)
	}

	fields := extractMappingFields(node)

	// Validate cpu if present
	if cpuNode, exists := fields["cpu"]; exists {
		if cpuNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s:%d %s.cpu must be int", filePath, cpuNode.Line, fieldName)
		}
	// Accept both int and string that can be parsed as int
	if _, err := strconv.Atoi(cpuNode.Value); err != nil {
		return fmt.Errorf("%s:%d %s.cpu must be int", filePath, cpuNode.Line, fieldName)
	}
}

	// Validate memory if present
	if memoryNode, exists := fields["memory"]; exists {
		if memoryNode.Kind != yaml.ScalarNode || memoryNode.Tag != "!!str" {
			return fmt.Errorf("%s:%d %s.memory must be string", filePath, memoryNode.Line, fieldName)
		}
		memory := memoryNode.Value
		if !validMemoryUnit.MatchString(memory) {
			return fmt.Errorf("%s:%d %s.memory has invalid format '%s'", filePath, memoryNode.Line, fieldName, memory)
		}
	}

	return nil
}

func extractMappingFields(node *yaml.Node) map[string]*yaml.Node {
	fields := make(map[string]*yaml.Node)
	if node.Kind != yaml.MappingNode {
		return fields
	}
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		fields[keyNode.Value] = valueNode
	}
	return fields
}