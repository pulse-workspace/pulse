// Package group — scim_groups.go: SCIM v2 (RFC 7644 §3.4) Group DTOs and
// conversion helpers for the SCIM /Groups endpoint set.
//
// These types are intentionally separate from the internal Group type so
// that internal schema changes (e.g., adding muted_until, settings blob)
// don't leak into the SCIM wire format, and so that SCIM-only fields
// (schemas URN, meta.location, external_id) don't pollute the projection.
//
// The wire DTOs are owned by the handlers package via re-export; keeping a
// parallel copy here lets the service layer return SCIM-shaped values
// without importing the handlers package (which would create a cycle:
// handlers → group → handlers).
package group

import "time"

// SCIMGroup is the RFC 7644 §3.4 representation of a Group resource.
type SCIMGroup struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id"`
	DisplayName string        `json:"displayName"`
	ExternalID  string        `json:"externalId,omitempty"`
	Meta        SCIMGroupMeta `json:"meta"`
	// Members is the SCIM members array, populated on PUT/PATCH input.
	Members []SCIMMember `json:"members,omitempty"`
}

// SCIMMember is a single member reference in a SCIM Group's members array.
type SCIMMember struct {
	Value   string `json:"value"`              // user ID
	Display string `json:"display,omitempty"`  // user display name (display only)
	Type    string `json:"type,omitempty"`     // "User" | "Group"
}

// SCIMGroupMeta is the RFC 7644 §3.4 meta sub-object for a Group.
type SCIMGroupMeta struct {
	ResourceType string    `json:"resourceType"`
	Created      time.Time `json:"created"`
	LastModified time.Time `json:"lastModified"`
	Location     string    `json:"location"`
}

// ListSCIMGroupsResponse is the SCIM v2 ListResponse envelope (§3.4.4).
type ListSCIMGroupsResponse struct {
	Schemas      []string    `json:"schemas"`
	TotalResults int         `json:"totalResults"`
	Resources    []SCIMGroup `json:"Resources"`
	StartIndex   int         `json:"startIndex"`
	ItemsPerPage int         `json:"itemsPerPage"`
}

// SCIMGroupSchema is the URN advertised in `schemas` for a Group resource.
const SCIMGroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"

// SCIMGroupListSchema is the URN advertised in `schemas` for a ListResponse
// of Group resources.
const SCIMGroupListSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse"

// SCIMPatchRequest is the RFC 7644 §3.5.2 PATCH request body.
//
// Per the SCIM spec, the `schemas` array MUST contain exactly the
// PatchOp URN ("urn:ietf:params:scim:api:messages:2.0:PatchOp") for a
// valid Patch request, but we tolerate any value (and an empty array)
// because IdPs in the wild get this wrong: Okta and Entra both send
// the PatchOp URN, but some older implementations omit `schemas`
// altogether. The handler validates operations, not `schemas`.
//
// Operations is an ordered list of patch operations applied in order.
// At least one operation is required; an empty list is rejected with
// 400 by the handler.
type SCIMPatchRequest struct {
	Schemas    []string             `json:"schemas"`
	Operations []SCIMPatchOperation `json:"Operations"`
}

// SCIMPatchOperation is one RFC 7644 §3.5.2 patch op entry.
//
// Per SCIM, `op` MUST be one of "add" | "remove" | "replace". We accept
// any string at the wire layer so the handler can return a clean
// 400 invalidValue with the list of supported ops (instead of an
// opaque JSON-decode error).
//
// `Path` is the SCIM path expression (RFC 7644 §3.5.2). For Group
// member ops the path is typically "members" or omitted (when
// `value` carries the member list). The handler currently accepts
// any path; only member-list operations are honored, all other paths
// are no-ops (RFC 7644 §3.5.2 — servers MAY ignore unsupported paths).
//
// `Value` is intentionally typed `any` because RFC 7644 §3.5.2 allows
// the value to be a JSON object, array, or scalar. The
// `extractMemberIDs` helper in the handlers package peels apart the
// common shapes (string, []string, []SCIMMemberRef, {members: []}).
type SCIMPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// SCIMMemberRef is a member reference object inside a PATCH value.
//
// SCIM v2 represents Group members as {"value": "<id>", "$ref": "...", "display": "..."}.
// `value` is the user identifier (SCIM-side), `display` is a
// human-readable label (often the user's name) that we ignore for
// membership bookkeeping. `$ref` is omitted from the wire format
// we accept because it is informational only (RFC 7644 §3.4 Group
// members).
type SCIMMemberRef struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

// SCIMPatchOpSchema is the URN advertised in `schemas` for a PATCH
// request body. RFC 7644 §3.5.2 mandates this be present.
const SCIMPatchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"