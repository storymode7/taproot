; queries/rust/symbols.scm
;
; Tree-sitter query for Rust symbol extraction.

; --- Function definitions ---
(function_item
  name: (identifier) @def.function)

; --- Impl method definitions ---
(impl_item
  body: (declaration_list
    (function_item
      name: (identifier) @def.function)))

; --- Struct, enum, trait, type alias definitions ---
(struct_item
  name: (type_identifier) @def.class)

(enum_item
  name: (type_identifier) @def.class)

(trait_item
  name: (type_identifier) @def.class)

(type_item
  name: (type_identifier) @def.class)

; --- Use declarations (imports) ---
(use_declaration
  argument: (scoped_identifier
    path: (identifier) @import.path))

(use_declaration
  argument: (use_as_clause
    path: (scoped_identifier) @import.path))

; --- Crate-level extern crate ---
(extern_crate_declaration
  name: (identifier) @import.path)

; --- Function calls ---
(call_expression
  function: (identifier) @call.name)

(call_expression
  function: (scoped_identifier
    name: (identifier) @call.name))

(call_expression
  function: (field_expression
    field: (field_identifier) @call.name))
