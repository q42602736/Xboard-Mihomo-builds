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

func TestMergeProfileYamlWithFormWritesDedicatedNodesForLegacyClient(t *testing.T) {
	const baseYaml = `xboard:
  provider: example
  ui:
    variant: new
`

	merged, err := mergeProfileYamlWithForm(baseYaml, ProfileFormState{
		HideDedicatedNodes: true,
	})
	if err != nil {
		t.Fatalf("合并专用节点配置失败: %v", err)
	}

	var decoded struct {
		XBoard struct {
			UI struct {
				HideDedicatedNodes bool `yaml:"hide_dedicated_nodes"`
			} `yaml:"ui"`
		} `yaml:"xboard"`
	}
	if err := yaml.Unmarshal([]byte(merged), &decoded); err != nil {
		t.Fatalf("解析专用节点配置失败: %v", err)
	}
	if !decoded.XBoard.UI.HideDedicatedNodes {
		t.Fatalf("legacy 档案未写入 hide_dedicated_nodes=true")
	}
}

func TestDedicatedNodesFeatureIsAvailableForLegacyClient(t *testing.T) {
	allowed := filterAllowedUIColorFeatureKeysForClient(
		[]string{customFeatureHideDedicatedNodes},
		buildClientLegacy,
	)
	if len(allowed) != 1 || allowed[0] != customFeatureHideDedicatedNodes {
		t.Fatalf("legacy 客户端可用自定义功能 = %v，期望包含专用节点显示配置", allowed)
	}
}

func TestWriteProfileUIColorCustomConfigWritesDedicatedNodesForLegacyProfile(t *testing.T) {
	const baseYaml = `xboard:
  provider: example
  ui:
    show_dedicated_nodes: false
`

	updated, err := writeProfileUIColorCustomConfig(baseYaml, UIColorCustomConfig{
		HideDedicatedNodes: true,
	})
	if err != nil {
		t.Fatalf("写入专用节点自定义配置失败: %v", err)
	}

	var decoded struct {
		XBoard struct {
			UI struct {
				HideDedicatedNodes bool  `yaml:"hide_dedicated_nodes"`
				ShowDedicatedNodes *bool `yaml:"show_dedicated_nodes"`
			} `yaml:"ui"`
		} `yaml:"xboard"`
	}
	if err := yaml.Unmarshal([]byte(updated), &decoded); err != nil {
		t.Fatalf("解析写入结果失败: %v", err)
	}
	if !decoded.XBoard.UI.HideDedicatedNodes {
		t.Fatalf("legacy 自定义配置未写入 hide_dedicated_nodes=true")
	}
	if decoded.XBoard.UI.ShowDedicatedNodes != nil {
		t.Fatalf("legacy 自定义配置未清理 show_dedicated_nodes 别名")
	}
}
