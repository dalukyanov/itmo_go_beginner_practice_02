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
	validMemoryUnit    = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)
	validContainerName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	validImageDomain   = "registry.bigbrother.io"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]
	errors := validatePodYAML(filePath)
	if len(errors) > 0 {
		for _, err := range errors {
			fmt.Fprintf(os.Stdout, "%s\n", err)
		}
		os.Exit(1)
	}
}

func validatePodYAML(filePath string) []string {
	var errors []string
	
	content, err := os.ReadFile(filePath)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot read file: %v", filePath, err)}
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return []string{fmt.Sprintf("%s: cannot unmarshal YAML: %v", filePath, err)}
	}

	var mappingNode *yaml.Node
	switch root.Kind {
	case yaml.DocumentNode:
		if len(root.Content) == 0 {
			return []string{fmt.Sprintf("%s: empty YAML document", filePath)}
		}
		if root.Content[0].Kind != yaml.MappingNode {
			return []string{fmt.Sprintf("%s: invalid YAML structure", filePath)}
		}
		mappingNode = root.Content[0]
	case yaml.MappingNode:
		mappingNode = &root
	default:
		return []string{fmt.Sprintf("%s: invalid YAML structure", filePath)}
	}

	fields := extractMappingFields(mappingNode)

	// Validate top-level fields
	if _, exists := fields["apiVersion"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: apiVersion is required", filePath))
	} else {
		if fields["apiVersion"].Value == "" {
			errors = append(errors, fmt.Sprintf("%s:%d apiVersion is required", filePath, fields["apiVersion"].Line))
		} else if fields["apiVersion"].Value != "v1" {
			errors = append(errors, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", filePath, fields["apiVersion"].Line, fields["apiVersion"].Value))
		}
	}

	if _, exists := fields["kind"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: kind is required", filePath))
	} else {
		if fields["kind"].Value == "" {
			errors = append(errors, fmt.Sprintf("%s:%d kind is required", filePath, fields["kind"].Line))
		} else if fields["kind"].Value != "Pod" {
			errors = append(errors, fmt.Sprintf("%s:%d kind has unsupported value '%s'", filePath, fields["kind"].Line, fields["kind"].Value))
		}
	}

	if _, exists := fields["metadata"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: metadata is required", filePath))
	} else {
		errors = append(errors, validateObjectMeta(filePath, fields["metadata"])...)
	}

	if _, exists := fields["spec"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: spec is required", filePath))
	} else {
		errors = append(errors, validatePodSpec(filePath, fields["spec"])...)
	}

	return errors
}

func validateObjectMeta(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d metadata must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if _, exists := fields["name"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: name is required", filePath))
	} else {
		if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d name must be string", filePath, fields["name"].Line))
		} else {
			name := fields["name"].Value
			if name == "" {
				errors = append(errors, fmt.Sprintf("%s:%d name is required", filePath, fields["name"].Line))
			}
		}
	}

	// namespace is optional
	if namespaceNode, exists := fields["namespace"]; exists {
		if namespaceNode.Kind != yaml.ScalarNode || namespaceNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d namespace must be string", filePath, namespaceNode.Line))
		}
	}

	// labels is optional
	if labelsNode, exists := fields["labels"]; exists {
		if labelsNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d labels must be object", filePath, labelsNode.Line))
		} else {
			// Validate that all label values are strings
			for i := 0; i < len(labelsNode.Content); i += 2 {
				valueNode := labelsNode.Content[i+1]
				if valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!str" {
					errors = append(errors, fmt.Sprintf("%s:%d labels values must be strings", filePath, valueNode.Line))
				}
			}
		}
	}

	return errors
}

func validatePodSpec(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d spec must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)

	// os is optional - handle both scalar and object formats
	if osNode, exists := fields["os"]; exists {
    	if osNode.Kind == yaml.ScalarNode {
        // Handle inline format: os: linux
        	osName := osNode.Value
        	if osName != validOSNameLinux && osName != validOSNameWindows {
            	errors = append(errors, fmt.Sprintf("%s:%d os has unsupported value '%s'", filePath, osNode.Line, osName))
        	}
    	} else if osNode.Kind == yaml.MappingNode {
        // Handle object format: os: {name: linux}
        	errors = append(errors, validatePodOS(filePath, osNode)...)
    	} else {
        errors = append(errors, fmt.Sprintf("%s:%d os must be string or object", filePath, osNode.Line))
    	}
	}

	if _, exists := fields["containers"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: containers is required", filePath))
	} else {
		errors = append(errors, validateContainers(filePath, fields["containers"])...)
	}

	return errors
}

func validatePodOS(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d os must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if fields["name"].Kind != yaml.ScalarNode {
    	errors = append(errors, fmt.Sprintf("%s:%d name must be string", filePath, fields["name"].Line))
	} else {
    	name := fields["name"].Value
    	if name == "" {
        	errors = append(errors, fmt.Sprintf("%s:%d name is required", filePath, fields["name"].Line))
    	} else if name != validOSNameLinux && name != validOSNameWindows {
        	errors = append(errors, fmt.Sprintf("%s:%d os has unsupported value '%s'", filePath, fields["name"].Line, name))
    	}
	}

	return errors
}

func validateContainers(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("%s:%d containers must be array", filePath, node.Line)}
	}

	containerNames := make(map[string]bool)
	for idx, containerNode := range node.Content {
		if containerNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d containers[%d] must be object", filePath, containerNode.Line, idx))
		} else {
			containerErrors := validateContainer(filePath, containerNode, containerNames)
			errors = append(errors, containerErrors...)
		}
	}

	return errors
}

func validateContainer(filePath string, node *yaml.Node, existingNames map[string]bool) []string {
	var errors []string
	
	fields := extractMappingFields(node)

	var name string
	if _, exists := fields["name"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: name is required", filePath))
	} else {
		if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d name must be string", filePath, fields["name"].Line))
		} else {
			name = fields["name"].Value
			if name == "" {
				errors = append(errors, fmt.Sprintf("%s:%d name is required", filePath, fields["name"].Line))
			} else {
				if !validContainerName.MatchString(name) {
					errors = append(errors, fmt.Sprintf("%s:%d name has invalid format '%s'", filePath, fields["name"].Line, name))
				} else {
					if existingNames[name] {
						errors = append(errors, fmt.Sprintf("%s:%d name must be unique in pod", filePath, fields["name"].Line))
					}
					existingNames[name] = true
				}
			}
		}
	}

	if _, exists := fields["image"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: image is required", filePath))
	} else {
		if fields["image"].Kind != yaml.ScalarNode || fields["image"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d image must be string", filePath, fields["image"].Line))
		} else {
			image := fields["image"].Value
			if image == "" {
				errors = append(errors, fmt.Sprintf("%s:%d image is required", filePath, fields["image"].Line))
			} else {
				// Check domain
				if !strings.HasPrefix(image, validImageDomain+"/") {
					errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image))
				} else {
					// Remove domain prefix to get the rest
					rest := strings.TrimPrefix(image, validImageDomain+"/")
					if rest == "" {
						errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image))
					} else {
						// Check if there's a tag (colon in the last part after last slash)
						lastSlashIndex := strings.LastIndex(rest, "/")
						var tagPart string
						if lastSlashIndex == -1 {
							// Format: registry.bigbrother.io/imagename:tag
							tagPart = rest
						} else {
							// Format: registry.bigbrother.io/namespace/imagename:tag
							tagPart = rest[lastSlashIndex+1:]
						}
						
						if !strings.Contains(tagPart, ":") {
							errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image))
						} else {
							// Ensure tag is not empty (e.g., "image:" is invalid)
							colonIndex := strings.Index(tagPart, ":")
							if colonIndex == len(tagPart)-1 {
								errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filePath, fields["image"].Line, image))
							}
						}
					}
				}
			}
		}
	}

	// ports is optional
	if portsNode, exists := fields["ports"]; exists {
		errors = append(errors, validateContainerPorts(filePath, portsNode)...)
	}

	// readinessProbe is optional
	if readinessProbeNode, exists := fields["readinessProbe"]; exists {
		errors = append(errors, validateProbe(filePath, readinessProbeNode)...)
	}

	// livenessProbe is optional
	if livenessProbeNode, exists := fields["livenessProbe"]; exists {
		errors = append(errors, validateProbe(filePath, livenessProbeNode)...)
	}

	if _, exists := fields["resources"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: resources is required", filePath))
	} else {
		errors = append(errors, validateResourceRequirements(filePath, fields["resources"])...)
	}

	return errors
}

func validateContainerPorts(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("%s:%d ports must be array", filePath, node.Line)}
	}

	for idx, portNode := range node.Content {
		if portNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d ports[%d] must be object", filePath, portNode.Line, idx))
		} else {
			errors = append(errors, validateContainerPort(filePath, portNode)...)
		}
	}

	return errors
}

func validateContainerPort(filePath string, node *yaml.Node) []string {
	var errors []string
	
	fields := extractMappingFields(node)

	if _, exists := fields["containerPort"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: containerPort is required", filePath))
	} else {
		if fields["containerPort"].Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", filePath, fields["containerPort"].Line))
		} else {
			if fields["containerPort"].Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", filePath, fields["containerPort"].Line))
			} else {
				port, err := strconv.Atoi(fields["containerPort"].Value)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", filePath, fields["containerPort"].Line))
				} else {
					if port <= 0 || port >= 65536 {
						errors = append(errors, fmt.Sprintf("%s:%d containerPort value out of range", filePath, fields["containerPort"].Line))
					}
				}
			}
		}
	}

	// protocol is optional
	if protocolNode, exists := fields["protocol"]; exists {
		if protocolNode.Kind != yaml.ScalarNode || protocolNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d protocol must be string", filePath, protocolNode.Line))
		} else {
			protocol := protocolNode.Value
			if protocol != validProtocolTCP && protocol != validProtocolUDP {
				errors = append(errors, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", filePath, protocolNode.Line, protocol))
			}
		}
	}

	return errors
}

func validateProbe(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d probe must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if _, exists := fields["httpGet"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: httpGet is required", filePath))
	} else {
		errors = append(errors, validateHTTPGetAction(filePath, fields["httpGet"])...)
	}

	return errors
}

func validateHTTPGetAction(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d httpGet must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)

	if _, exists := fields["path"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: path is required", filePath))
	} else {
		if fields["path"].Kind != yaml.ScalarNode || fields["path"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d path must be string", filePath, fields["path"].Line))
		} else {
			path := fields["path"].Value
			if path == "" {
				errors = append(errors, fmt.Sprintf("%s:%d path is required", filePath, fields["path"].Line))
			} else if !strings.HasPrefix(path, "/") {
				errors = append(errors, fmt.Sprintf("%s:%d path must be absolute", filePath, fields["path"].Line))
			}
		}
	}

	if _, exists := fields["port"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: port is required", filePath))
	} else {
		if fields["port"].Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d port must be int", filePath, fields["port"].Line))
		} else {
			if fields["port"].Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d port must be int", filePath, fields["port"].Line))
			} else {
				port, err := strconv.Atoi(fields["port"].Value)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d port must be int", filePath, fields["port"].Line))
				} else {
					if port <= 0 || port >= 65536 {
						errors = append(errors, fmt.Sprintf("%s:%d port value out of range", filePath, fields["port"].Line))
					}
				}
			}
		}
	}

	return errors
}

func validateResourceRequirements(filePath string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d resources must be object", filePath, node.Line)}
	}

	fields := extractMappingFields(node)

	// requests is optional
	if requestsNode, exists := fields["requests"]; exists {
		errors = append(errors, validateResourceList(filePath, requestsNode, "requests")...)
	}

	// limits is optional
	if limitsNode, exists := fields["limits"]; exists {
		errors = append(errors, validateResourceList(filePath, limitsNode, "limits")...)
	}

	return errors
}

func validateResourceList(filePath string, node *yaml.Node, fieldName string) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d %s must be object", filePath, node.Line, fieldName)}
	}

	fields := extractMappingFields(node)

	// Validate cpu if present
	if cpuNode, exists := fields["cpu"]; exists {
		if cpuNode.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d %s.cpu must be int", filePath, cpuNode.Line, fieldName))
		} else {
			if cpuNode.Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d %s.cpu must be int", filePath, cpuNode.Line, fieldName))
			} else {
				if _, err := strconv.Atoi(cpuNode.Value); err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d %s.cpu must be int", filePath, cpuNode.Line, fieldName))
				}
			}
		}
	}

	// Validate memory if present
	if memoryNode, exists := fields["memory"]; exists {
		if memoryNode.Kind != yaml.ScalarNode || memoryNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d %s.memory must be string", filePath, memoryNode.Line, fieldName))
		} else {
			memory := memoryNode.Value
			if memory == "" {
				errors = append(errors, fmt.Sprintf("%s:%d %s.memory is required", filePath, memoryNode.Line, fieldName))
			} else if !validMemoryUnit.MatchString(memory) {
				errors = append(errors, fmt.Sprintf("%s:%d %s.memory has invalid format '%s'", filePath, memoryNode.Line, fieldName, memory))
			}
		}
	}

	return errors
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