package app

import "sort"

// selectOptionsWithSelected marks the matching option and preserves submitted custom values.
func selectOptionsWithSelected(options []uiSelectOptionView, selected string) []uiSelectOptionView {
	found := false
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
		found = found || options[idx].Selected
	}
	if selected != "" && !found {
		options = append(options, uiSelectOptionView{Value: selected, Label: selected, Selected: true})
	}
	return options
}

// selectOptionsFromLabels converts a label map into value-sorted select options.
func selectOptionsFromLabels(labels map[string]string, selected string) []uiSelectOptionView {
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	options := make([]uiSelectOptionView, 0, len(ids))
	for _, id := range ids {
		options = append(options, uiSelectOptionView{
			Value:    id,
			Label:    labels[id],
			Selected: id == selected,
		})
	}
	return options
}
