// This is a deliberately separate Go module, not a member of the root
// NetRewind module. PRODUCT_RELEASE_PLAN_AR.md §5 Phase 5A calls this an
// "isolated spike, do not integrate it early" - it exists to find out what is
// actually true about Windows's IP Helper notification APIs on a real
// machine, not to become production code. Keeping it a separate module means
// it cannot accidentally get imported by anything under internal/ or cmd/,
// and its own go.mod/go.sum churn never touches the root module's.
module github.com/OmarAlghafri/netrewind/spikes/windows-ip-helper

go 1.25.0

require golang.org/x/sys v0.47.0
