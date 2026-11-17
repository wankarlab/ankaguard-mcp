// Package edition reports which product edition this binary was compiled as.
//
// The edition is fixed at build time by Go build tags:
//
//	(no tag)      -> "standard"     current behaviour of this repository
//	product       -> "product"      Community build of the product
//	product,pro   -> "product-pro"  product build with the Pro components
//
// The "pro" tag on its own changes nothing: Pro only makes sense on top of
// the product build.
package edition

// Build names, as exposed by GET /api/v1/product/status.
const (
	BuildStandard   = "standard"
	BuildProduct    = "product"
	BuildProductPro = "product-pro"
)

// Build returns the build name: "standard", "product" or "product-pro".
func Build() string { return build }

// IsProduct reports whether the binary was compiled with the product tag.
func IsProduct() bool { return build != BuildStandard }

// IsPro reports whether the binary includes the Pro components (product,pro).
func IsPro() bool { return build == BuildProductPro }
