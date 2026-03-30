package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type RankingConfig struct {
	DocTypes map[string]DocTypeRanking `yaml:"doctypes"`
	Global   GlobalRanking             `yaml:"global"`
}

type DocTypeRanking struct {
	SearchableAttributes []string            `yaml:"searchable_attributes"`
	Synonyms             map[string][]string `yaml:"synonyms"`
}

type GlobalRanking struct {
	RankingRules []string `yaml:"ranking_rules"`
}

// LoadRanking parses the ranking.yaml configuration file.
func LoadRanking(path string) (*RankingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default configuration if file is missing
			return &RankingConfig{
				Global: GlobalRanking{
					RankingRules: []string{"words", "typo", "proximity", "attribute", "sort", "exactness"},
				},
			}, nil
		}
		return nil, err
	}

	var cfg RankingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
