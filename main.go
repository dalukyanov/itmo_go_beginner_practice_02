package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		fmt.Printf("Usage: %s <yaml-file>", os.Args[0])
		os.Exit(255)
	}

	filePath := os.Args[1]
	fileName := filepath.Base(filePath)
	errors := validatePodYAML(filePath, fileName)
	if len(errors) > 0 {
		// Reverse the slice
		for i, j := 0, len(errors)-1; i < j; i, j = i+1, j-1 {
			errors[i], errors[j] = errors[j], errors[i]
		}
		for _, err := range errors {
			fmt.Println(err)
		}
		os.Exit(255)
	}
}

func validatePodYAML(fullPath string, fileName string) []string {
	var errors []string
	
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot read file: %v", fileName, err)}
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return []string{fmt.Sprintf("%s: cannot unmarshal YAML: %v", fileName, err)}
	}

	var mappingNode *yaml.Node
	switch root.Kind {
	case yaml.DocumentNode:
		if len(root.Content) == 0 {
			return []string{fmt.Sprintf("%s: empty YAML document", fileName)}
		}
		if root.Content[0].Kind != yaml.MappingNode {
			return []string{fmt.Sprintf("%s: invalid YAML structure", fileName)}
		}
		mappingNode = root.Content[0]
	case yaml.MappingNode:
		mappingNode = &root
	default:
		return []string{fmt.Sprintf("%s: invalid YAML structure", fileName)}
	}

	fields := extractMappingFields(mappingNode)

	// Validate top-level fields
	if _, exists := fields["apiVersion"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: apiVersion is required", fileName))
	} else {
		if fields["apiVersion"].Value == "" {
			errors = append(errors, fmt.Sprintf("%s:%d apiVersion is required", fileName, fields["apiVersion"].Line))
		} else if fields["apiVersion"].Value != "v1" {
			errors = append(errors, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", fileName, fields["apiVersion"].Line, fields["apiVersion"].Value))
		}
	}

	if _, exists := fields["kind"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: kind is required", fileName))
	} else {
		if fields["kind"].Value == "" {
			errors = append(errors, fmt.Sprintf("%s:%d kind is required", fileName, fields["kind"].Line))
		} else if fields["kind"].Value != "Pod" {
			errors = append(errors, fmt.Sprintf("%s:%d kind has unsupported value '%s'", fileName, fields["kind"].Line, fields["kind"].Value))
		}
	}

	if _, exists := fields["metadata"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: metadata is required", fileName))
	} else {
		errors = append(errors, validateObjectMeta(fileName, fields["metadata"])...)
	}

	if _, exists := fields["spec"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: spec is required", fileName))
	} else {
		errors = append(errors, validatePodSpec(fileName, fields["spec"])...)
	}

	return errors
}

func validateObjectMeta(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d metadata must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if _, exists := fields["name"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: name is required", fileName))
	} else {
		if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d name must be string", fileName, fields["name"].Line))
		} else {
			name := fields["name"].Value
			if name == "" {
				errors = append(errors, fmt.Sprintf("%s:%d name is required", fileName, fields["name"].Line))
			}
		}
	}

	// namespace is optional
	if namespaceNode, exists := fields["namespace"]; exists {
		if namespaceNode.Kind != yaml.ScalarNode || namespaceNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d namespace must be string", fileName, namespaceNode.Line))
		}
	}

	// labels is optional
	if labelsNode, exists := fields["labels"]; exists {
		if labelsNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d labels must be object", fileName, labelsNode.Line))
		} else {
			// Validate that all label values are strings
			for i := 0; i < len(labelsNode.Content); i += 2 {
				valueNode := labelsNode.Content[i+1]
				if valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!str" {
					errors = append(errors, fmt.Sprintf("%s:%d labels values must be strings", fileName, valueNode.Line))
				}
			}
		}
	}

	return errors
}

func validatePodSpec(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d spec must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)

	// os is optional - handle both scalar and object formats
	if osNode, exists := fields["os"]; exists {
    	if osNode.Kind == yaml.ScalarNode {
        // Handle inline format: os: linux
        	osName := osNode.Value
        	if osName != validOSNameLinux && osName != validOSNameWindows {
            	errors = append(errors, fmt.Sprintf("%s:%d os has unsupported value '%s'", fileName, osNode.Line, osName))
        	}
    	} else if osNode.Kind == yaml.MappingNode {
        // Handle object format: os: {name: linux}
        	errors = append(errors, validatePodOS(fileName, osNode)...)
    	} else {
        errors = append(errors, fmt.Sprintf("%s:%d os must be string or object", fileName, osNode.Line))
    	}
	}

	if _, exists := fields["containers"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: containers is required", fileName))
	} else {
		errors = append(errors, validateContainers(fileName, fields["containers"])...)
	}

	return errors
}

func validatePodOS(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d os must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if fields["name"].Kind != yaml.ScalarNode {
    	errors = append(errors, fmt.Sprintf("%s:%d name must be string", fileName, fields["name"].Line))
	} else {
    	name := fields["name"].Value
    	if name == "" {
        	errors = append(errors, fmt.Sprintf("%s:%d name is required", fileName, fields["name"].Line))
    	} else if name != validOSNameLinux && name != validOSNameWindows {
        	errors = append(errors, fmt.Sprintf("%s:%d os has unsupported value '%s'", fileName, fields["name"].Line, name))
    	}
	}

	return errors
}

func validateContainers(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("%s:%d containers must be array", fileName, node.Line)}
	}

	containerNames := make(map[string]bool)
	for idx, containerNode := range node.Content {
		if containerNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d containers[%d] must be object", fileName, containerNode.Line, idx))
		} else {
			containerErrors := validateContainer(fileName, containerNode, containerNames)
			errors = append(errors, containerErrors...)
		}
	}

	return errors
}

func validateContainer(fileName string, node *yaml.Node, existingNames map[string]bool) []string {
	var errors []string
	
	fields := extractMappingFields(node)

	var name string
	if _, exists := fields["name"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: name is required", fileName))
	} else {
		if fields["name"].Kind != yaml.ScalarNode || fields["name"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d name must be string", fileName, fields["name"].Line))
		} else {
			name = fields["name"].Value
			if name == "" {
				errors = append(errors, fmt.Sprintf("%s:%d name is required", fileName, fields["name"].Line))
			} else {
				if !validContainerName.MatchString(name) {
					errors = append(errors, fmt.Sprintf("%s:%d name has invalid format '%s'", fileName, fields["name"].Line, name))
				} else {
					if existingNames[name] {
						errors = append(errors, fmt.Sprintf("%s:%d name must be unique in pod", fileName, fields["name"].Line))
					}
					existingNames[name] = true
				}
			}
		}
	}

	if _, exists := fields["image"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: image is required", fileName))
	} else {
		if fields["image"].Kind != yaml.ScalarNode || fields["image"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d image must be string", fileName, fields["image"].Line))
		} else {
			image := fields["image"].Value
			if image == "" {
				errors = append(errors, fmt.Sprintf("%s:%d image is required", fileName, fields["image"].Line))
			} else {
				// Check domain
				if !strings.HasPrefix(image, validImageDomain+"/") {
					errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", fileName, fields["image"].Line, image))
				} else {
					// Remove domain prefix to get the rest
					rest := strings.TrimPrefix(image, validImageDomain+"/")
					if rest == "" {
						errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", fileName, fields["image"].Line, image))
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
							errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", fileName, fields["image"].Line, image))
						} else {
							// Ensure tag is not empty (e.g., "image:" is invalid)
							colonIndex := strings.Index(tagPart, ":")
							if colonIndex == len(tagPart)-1 {
								errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", fileName, fields["image"].Line, image))
							}
						}
					}
				}
			}
		}
	}

	// ports is optional
	if portsNode, exists := fields["ports"]; exists {
		errors = append(errors, validateContainerPorts(fileName, portsNode)...)
	}

	// readinessProbe is optional
	if readinessProbeNode, exists := fields["readinessProbe"]; exists {
		errors = append(errors, validateProbe(fileName, readinessProbeNode)...)
	}

	// livenessProbe is optional
	if livenessProbeNode, exists := fields["livenessProbe"]; exists {
		errors = append(errors, validateProbe(fileName, livenessProbeNode)...)
	}

	if _, exists := fields["resources"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: resources is required", fileName))
	} else {
		errors = append(errors, validateResourceRequirements(fileName, fields["resources"])...)
	}

	return errors
}

func validateContainerPorts(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("%s:%d ports must be array", fileName, node.Line)}
	}

	for idx, portNode := range node.Content {
		if portNode.Kind != yaml.MappingNode {
			errors = append(errors, fmt.Sprintf("%s:%d ports[%d] must be object", fileName, portNode.Line, idx))
		} else {
			errors = append(errors, validateContainerPort(fileName, portNode)...)
		}
	}

	return errors
}

func validateContainerPort(fileName string, node *yaml.Node) []string {
	var errors []string
	
	fields := extractMappingFields(node)

	if _, exists := fields["containerPort"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: containerPort is required", fileName))
	} else {
		if fields["containerPort"].Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", fileName, fields["containerPort"].Line))
		} else {
			if fields["containerPort"].Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", fileName, fields["containerPort"].Line))
			} else {
				port, err := strconv.Atoi(fields["containerPort"].Value)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", fileName, fields["containerPort"].Line))
				} else {
					if port <= 0 || port >= 65536 {
						errors = append(errors, fmt.Sprintf("%s:%d containerPort value out of range", fileName, fields["containerPort"].Line))
					}
				}
			}
		}
	}

	// protocol is optional
	if protocolNode, exists := fields["protocol"]; exists {
		if protocolNode.Kind != yaml.ScalarNode || protocolNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d protocol must be string", fileName, protocolNode.Line))
		} else {
			protocol := protocolNode.Value
			if protocol != validProtocolTCP && protocol != validProtocolUDP {
				errors = append(errors, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", fileName, protocolNode.Line, protocol))
			}
		}
	}

	return errors
}

func validateProbe(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d probe must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)
	
	if _, exists := fields["httpGet"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: httpGet is required", fileName))
	} else {
		errors = append(errors, validateHTTPGetAction(fileName, fields["httpGet"])...)
	}

	return errors
}

func validateHTTPGetAction(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d httpGet must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)

	if _, exists := fields["path"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: path is required", fileName))
	} else {
		if fields["path"].Kind != yaml.ScalarNode || fields["path"].Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d path must be string", fileName, fields["path"].Line))
		} else {
			path := fields["path"].Value
			if path == "" {
				errors = append(errors, fmt.Sprintf("%s:%d path is required", fileName, fields["path"].Line))
			} else if !strings.HasPrefix(path, "/") {
				errors = append(errors, fmt.Sprintf("%s:%d path must be absolute", fileName, fields["path"].Line))
			}
		}
	}

	if _, exists := fields["port"]; !exists {
		errors = append(errors, fmt.Sprintf("%s: port is required", fileName))
	} else {
		if fields["port"].Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d port must be int", fileName, fields["port"].Line))
		} else {
			if fields["port"].Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d port must be int", fileName, fields["port"].Line))
			} else {
				port, err := strconv.Atoi(fields["port"].Value)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d port must be int", fileName, fields["port"].Line))
				} else {
					if port <= 0 || port >= 65536 {
						errors = append(errors, fmt.Sprintf("%s:%d port value out of range", fileName, fields["port"].Line))
					}
				}
			}
		}
	}

	return errors
}

func validateResourceRequirements(fileName string, node *yaml.Node) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d resources must be object", fileName, node.Line)}
	}

	fields := extractMappingFields(node)

	// requests is optional
	if requestsNode, exists := fields["requests"]; exists {
		errors = append(errors, validateResourceList(fileName, requestsNode, "requests")...)
	}

	// limits is optional
	if limitsNode, exists := fields["limits"]; exists {
		errors = append(errors, validateResourceList(fileName, limitsNode, "limits")...)
	}

	return errors
}

func validateResourceList(fileName string, node *yaml.Node, fieldName string) []string {
	var errors []string
	
	if node.Kind != yaml.MappingNode {
		return []string{fmt.Sprintf("%s:%d %s must be object", fileName, node.Line, fieldName)}
	}

	fields := extractMappingFields(node)

	// Validate cpu if present
	if cpuNode, exists := fields["cpu"]; exists {
		if cpuNode.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", fileName, cpuNode.Line))
		} else {
			if cpuNode.Tag != "!!int" {
				errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", fileName, cpuNode.Line))
			} else {
				if _, err := strconv.Atoi(cpuNode.Value); err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", fileName, cpuNode.Line))
				}
			}
		}
	}

	// Validate memory if present
	if memoryNode, exists := fields["memory"]; exists {
		if memoryNode.Kind != yaml.ScalarNode || memoryNode.Tag != "!!str" {
			errors = append(errors, fmt.Sprintf("%s:%d memory must be string", fileName, memoryNode.Line))
		} else {
			memory := memoryNode.Value
			if memory == "" {
				errors = append(errors, fmt.Sprintf("%s:%d memory is required", fileName, memoryNode.Line))
			} else if !validMemoryUnit.MatchString(memory) {
				errors = append(errors, fmt.Sprintf("%s:%d memory has invalid format '%s'", fileName, memoryNode.Line, memory))
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