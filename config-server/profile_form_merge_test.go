package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeProfileYamlWithFormWritesUiVariantForLegacyClient(t *testing.T) {
	const baseYaml = `xboard:
  provider: example
  ui:
    variant: legacy
    home_panel_default_layout: layout1
`

	merged, err := mergeProfileYamlWithForm(baseYaml, ProfileFormState{
		UiVariant:              "new",
		HomePanelDefaultLayout: "layout1",
	})
	if err != nil {
		t.Fatalf("合并配置失败: %v", err)
	}

	var decoded struct {
		XBoard struct {
			UI struct {
				Variant                string `yaml:"variant"`
				HomePanelDefaultLayout string `yaml:"home_panel_default_layout"`
			} `yaml:"ui"`
		} `yaml:"xboard"`
	}
	if err := yaml.Unmarshal([]byte(merged), &decoded); err != nil {
		t.Fatalf("解析合并结果失败: %v", err)
	}

	if decoded.XBoard.UI.Variant != "new" {
		t.Fatalf("UI 构建变体 = %q，期望 new", decoded.XBoard.UI.Variant)
	}
	if decoded.XBoard.UI.HomePanelDefaultLayout != "layout1" {
		t.Fatalf("首页布局 = %q，期望 layout1", decoded.XBoard.UI.HomePanelDefaultLayout)
	}
}
