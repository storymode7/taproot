; queries/go/symbols.scm
;
; Tree-sitter query for Go symbol extraction.

; --- Function declarations ---
(function_declaration
  name: (identifier) @def.function)

; --- Method declarations ---
(method_declaration
  name: (field_identifier) @def.function)

; --- Type declarations (struct, interface, type alias) ---
(type_declaration
  (type_spec
    name: (type_identifier) @def.class))

; --- Import paths ---
(import_spec
  path: (interpreted_string_literal) @import.path)

; --- Function calls ---
(call_expression
  function: (identifier) @call.name)

(call_expression
  function: (selector_expression
    field: (field_identifier) @call.name))
