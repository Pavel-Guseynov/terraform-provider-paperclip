package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestScopedToolAccessResourcesAreRegistered(t *testing.T) {
	want := map[string]bool{"paperclip_tool_profile": false, "paperclip_tool_profile_binding": false}
	for _, factory := range New("test")().(*PaperclipProvider).Resources(context.Background()) {
		var response resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "paperclip"}, &response)
		if _, ok := want[response.TypeName]; ok {
			want[response.TypeName] = true
		}
	}
	for name, registered := range want {
		if !registered {
			t.Errorf("%s is missing: scoped profile access cannot be managed", name)
		}
	}
}
