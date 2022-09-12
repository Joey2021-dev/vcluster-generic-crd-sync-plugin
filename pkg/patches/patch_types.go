package patches

import (
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/loft-sh/vcluster-generic-crd-plugin/pkg/config"
	yamlhelper "github.com/loft-sh/vcluster-generic-crd-plugin/pkg/util/yaml"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v3"
)

const (
	RegexNameGroup      = "NAME"
	RegexNamespaceGroup = "NAMESPACE"
)

func CopyFromObject(obj1, obj2 *yaml.Node, patch *config.Patch) error {
	if obj2 == nil {
		return nil
	}

	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	fromPath := patch.FromPath
	if fromPath == "" {
		fromPath = patch.Path
	}
	fromMatches, err := FindMatches(obj2, fromPath)
	if err != nil {
		return errors.Wrap(err, "find from matches")
	} else if len(fromMatches) > 1 {
		return fmt.Errorf("more than 1 match found for path %s", fromPath)
	}

	if len(fromMatches) == 1 && len(matches) == 0 {
		validated, err := ValidateAllConditions(obj1, nil, patch.Conditions)
		if err != nil {
			return errors.Wrap(err, "validate conditions")
		} else if !validated {
			return nil
		}

		return createPath(obj1, patch.Path, fromMatches[0])
	}

	for _, m := range matches {
		validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
		if err != nil {
			return errors.Wrap(err, "validate conditions")
		} else if !validated {
			continue
		}

		if len(fromMatches) == 1 {
			ReplaceNode(obj1, m, &yaml.Node{
				Kind: yaml.DocumentNode,
				Content: []*yaml.Node{
					fromMatches[0],
				},
			})
		} else {
			parent := Find(obj1, ContainsChild(m))
			removeChild(parent, m)
		}
	}

	return nil
}

func Remove(obj1 *yaml.Node, patch *config.Patch) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	for _, m := range matches {
		validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
		if err != nil {
			return errors.Wrap(err, "validate conditions")
		} else if !validated {
			continue
		}

		parent := Find(obj1, ContainsChild(m))
		switch parent.Kind {
		case yaml.MappingNode:
			parent.Content = removeProperty(parent, m)
		case yaml.SequenceNode:
			parent.Content = removeChild(parent, m)
		}
	}

	return nil
}

func Add(obj1 *yaml.Node, patch *config.Patch) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	value, err := NewNode(patch.Value)
	if err != nil {
		return errors.Wrap(err, "new node from value")
	}

	if len(matches) == 0 {
		validated, err := ValidateAllConditions(obj1, nil, patch.Conditions)
		if err != nil {
			return errors.Wrap(err, "validate conditions")
		} else if !validated {
			return nil
		}

		err = createPath(obj1, patch.Path, value)
		if err != nil {
			return err
		}
	} else {
		for _, m := range matches {
			validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
			if err != nil {
				return errors.Wrap(err, "validate conditions")
			} else if !validated {
				continue
			}

			AddNode(obj1, m, value)
		}
	}

	return nil
}

func Replace(obj1 *yaml.Node, patch *config.Patch) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	value, err := NewNode(patch.Value)
	if err != nil {
		return errors.Wrap(err, "new node from value")
	}

	for _, m := range matches {
		validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
		if err != nil {
			return errors.Wrap(err, "validate conditions")
		} else if !validated {
			continue
		}

		ReplaceNode(obj1, m, value)
	}

	return nil
}

func RewriteName(obj1 *yaml.Node, patch *config.Patch, resolver NameResolver) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	for _, m := range matches {
		if m.Kind == yaml.ScalarNode {
			validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
			if err != nil {
				return errors.Wrap(err, "validate conditions")
			} else if !validated {
				continue
			}

			translatedName, err := resolver.TranslateName(m.Value, patch.ParsedRegex, patch.FromPath)
			if err != nil {
				return err
			}

			newNode, err := NewNode(translatedName)
			if err != nil {
				return errors.Wrap(err, "create node")
			}

			ReplaceNode(obj1, m, newNode)
		}
	}

	return nil
}

func RewriteLabelSelector(obj1 *yaml.Node, patch *config.Patch, resolver NameResolver) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	for _, m := range matches {
		if m.Kind == yaml.MappingNode {
			validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
			if err != nil {
				return errors.Wrap(err, "validate conditions")
			} else if !validated {
				continue
			}

			yamlString, err := yaml.Marshal(m)
			if err != nil {
				return errors.Wrap(err, "marshal label selector")
			}

			// try to unmarshal into label selector first
			var newNode *yaml.Node
			labelSelector := map[string]string{}
			err = yamlhelper.UnmarshalStrict(yamlString, &labelSelector)
			if err != nil {
				return errors.Wrap(err, "unmarshal label selector")
			}

			// translate label selector
			labelSelector, err = resolver.TranslateLabelSelector(labelSelector)
			if err != nil {
				return err
			}

			newNode, err = NewNode(labelSelector)
			if err != nil {
				return errors.Wrap(err, "create node")
			}

			ReplaceNode(obj1, m, newNode)
		}
	}

	return nil
}

func RewriteLabelExpressionsSelector(obj1 *yaml.Node, patch *config.Patch, resolver NameResolver) error {
	matches, err := FindMatches(obj1, patch.Path)
	if err != nil {
		return errors.Wrap(err, "find matches")
	}

	for _, m := range matches {
		if m.Kind == yaml.MappingNode {
			validated, err := ValidateAllConditions(obj1, m, patch.Conditions)
			if err != nil {
				return errors.Wrap(err, "validate conditions")
			} else if !validated {
				continue
			}

			yamlString, err := yaml.Marshal(m)
			if err != nil {
				return errors.Wrap(err, "marshal label selector")
			}

			// try to unmarshal into label selector first
			var newNode *yaml.Node
			labelSelector := &metav1.LabelSelector{}
			err = yamlhelper.UnmarshalStrict(yamlString, labelSelector)
			if err != nil {
				return errors.Wrap(err, "unmarshal label selector")
			}

			// translate label expressions selector
			labelSelector, err = resolver.TranslateLabelExpressionsSelector(labelSelector)
			if err != nil {
				return err
			}

			newNode, err = NewNode(labelSelector)
			if err != nil {
				return errors.Wrap(err, "create node")
			}

			ReplaceNode(obj1, m, newNode)
		}
	}

	return nil
}

func createPath(obj1 *yaml.Node, path string, value *yaml.Node) error {
	// unpack document nodes
	if value != nil && value.Kind == yaml.DocumentNode {
		value = value.Content[0]
	}

	opPath := OpPath(path)
	matches, err := getParents(obj1, opPath)
	if err != nil {
		return fmt.Errorf("could not replace using path: %s", path)
	} else if len(matches) == 0 {
		// are we at the top path?
		parentPath := opPath.getParentPath()
		if path == parentPath || parentPath == "" || path == "" {
			return nil
		}

		if isSequenceChild(path) {
			value = createSequenceNode(path, value)
		} else {
			value = createMappingNode(path, value)
		}

		return createPath(obj1, parentPath, value)
	}

	// check if we expect an array or map as parent
	for _, match := range matches {
		parent := Find(obj1, ContainsChild(match))
		switch match.Kind {
		case yaml.ScalarNode:
			parent.Content = AddChildAtIndex(parent, match, value)
		case yaml.MappingNode:
			match.Content = append(match.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: opPath.getChildName(),
			}, value)
		case yaml.SequenceNode:
			match.Content = append(match.Content, value)
		case yaml.DocumentNode:
			match.Content[0].Content = append(match.Content[0].Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: opPath.getChildName(),
			}, value)
		}
	}

	return nil
}

func isSequenceChild(path string) bool {
	opPath := OpPath(path)
	propertyName := opPath.getChildName()
	if propertyName == "" {
		return false
	}
	_, err := strconv.Atoi(propertyName)
	return err == nil
}

func createSequenceNode(path string, child *yaml.Node) *yaml.Node {
	childNode := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
	}
	if child != nil {
		childNode.Content = append(
			childNode.Content,
			child,
		)
	}
	return childNode
}

func createMappingNode(path string, child *yaml.Node) *yaml.Node {
	opPath := OpPath(path)
	childNode := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}
	if child != nil {
		childNode.Content = append(
			childNode.Content,
			&yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: opPath.getChildName(),
				Tag:   "!!str",
			},
			child,
		)
	}

	return childNode
}

func AddNode(obj1 *yaml.Node, match *yaml.Node, value *yaml.Node) {
	parent := Find(obj1, ContainsChild(match))
	switch match.Kind {
	case yaml.ScalarNode:
		parent.Content = AddChildAtIndex(parent, match, value)
	case yaml.MappingNode:
		match.Content = append(match.Content, value.Content[0].Content...)
	case yaml.SequenceNode:
		match.Content = append(match.Content, value.Content...)
	case yaml.DocumentNode:
		match.Content[0].Content = append(match.Content[0].Content, value.Content[0].Content...)
	}
}
