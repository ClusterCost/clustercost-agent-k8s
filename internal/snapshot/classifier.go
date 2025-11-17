package snapshot

import "strings"

// ClassifierConfig describes how to map namespaces into environments.
type ClassifierConfig struct {
	LabelKeys              []string
	ProductionLabelValues  []string
	NonProdLabelValues     []string
	SystemLabelValues      []string
	ProductionNameContains []string
	SystemNamespaces       []string
}

// EnvironmentClassifier applies the configured heuristics.
type EnvironmentClassifier struct {
	labelKeys              []string
	labelValueMap          map[string]string
	productionNameContains []string
	systemNamespaces       map[string]struct{}
}

// NewEnvironmentClassifier builds a classifier with normalized lookups.
func NewEnvironmentClassifier(cfg ClassifierConfig) *EnvironmentClassifier {
	labelKeys := cfg.LabelKeys
	if len(labelKeys) == 0 {
		labelKeys = []string{"clustercost.io/environment"}
	}
	labelValueMap := map[string]string{}
	for _, v := range cfg.ProductionLabelValues {
		labelValueMap[strings.ToLower(v)] = "production"
	}
	for _, v := range cfg.NonProdLabelValues {
		labelValueMap[strings.ToLower(v)] = "nonprod"
	}
	for _, v := range cfg.SystemLabelValues {
		labelValueMap[strings.ToLower(v)] = "system"
	}

	sysNamespaces := make(map[string]struct{}, len(cfg.SystemNamespaces))
	for _, ns := range cfg.SystemNamespaces {
		sysNamespaces[strings.ToLower(ns)] = struct{}{}
	}

	prodContains := make([]string, 0, len(cfg.ProductionNameContains))
	for _, needle := range cfg.ProductionNameContains {
		prodContains = append(prodContains, strings.ToLower(needle))
	}
	if len(prodContains) == 0 {
		prodContains = []string{"prod"}
	}

	return &EnvironmentClassifier{
		labelKeys:              labelKeys,
		labelValueMap:          labelValueMap,
		productionNameContains: prodContains,
		systemNamespaces:       sysNamespaces,
	}
}

// Classify returns the best-effort environment assignment for the namespace.
func (c *EnvironmentClassifier) Classify(name string, labels map[string]string) string {
	if labels != nil {
		for _, key := range c.labelKeys {
			if key == "" {
				continue
			}
			if value, ok := labels[key]; ok && value != "" {
				if env, found := c.labelValueMap[strings.ToLower(value)]; found {
					return env
				}
				return "unknown"
			}
		}
	}

	lowerName := strings.ToLower(name)
	for _, needle := range c.productionNameContains {
		if needle != "" && strings.Contains(lowerName, needle) {
			return "production"
		}
	}
	if _, ok := c.systemNamespaces[lowerName]; ok {
		return "system"
	}
	return "nonprod"
}
