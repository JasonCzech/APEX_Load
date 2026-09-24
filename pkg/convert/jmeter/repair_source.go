package jmeter

import "fmt"

// ValidateOAuthNormalization inspects only the selected source configuration.
// No source credential value is returned or included in errors.
func ValidateOAuthNormalization(source []byte, configurationID string) error {
	root, err := parseXML(source)
	if err != nil {
		return fmt.Errorf("cannot inspect protected JMX source")
	}
	var selected *xmlNode
	var visit func(*xmlNode)
	visit = func(n *xmlNode) {
		if n.Name == diKafkaConfig && messagingNodeID(sourcePlanID(source), n) == configurationID {
			selected = n
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(root)
	if selected == nil {
		return fmt.Errorf("source configuration not found")
	}
	raw := ""
	for _, collection := range childProps(selected, "extraConfigs") {
		for _, row := range collection.Children {
			if prop(row, "configKey") == "sasl.jaas.config" {
				raw = prop(row, "configValue")
			}
		}
	}
	opts, err := parseMessagingOAuthJAAS(raw)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{"cid", "clientId"}, {"cs", "clientSecret"}} {
		a, old := opts[pair[0]]
		b, current := opts[pair[1]]
		if old && current && a != b {
			return fmt.Errorf("conflicting legacy and standard OAuth options")
		}
	}
	if _, ok := opts["cid"]; !ok {
		if _, ok = opts["cs"]; !ok {
			return fmt.Errorf("no legacy OAuth options to normalize")
		}
	}
	return nil
}
