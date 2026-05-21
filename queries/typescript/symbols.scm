; queries/typescript/symbols.scm
;
; Tree-sitter query for TypeScript symbol extraction.
; Captures: function/method definitions, class definitions, import paths.
; Used by the ordering engine to build the cross-file symbol graph.
;
; Capture names:
;   @def.function  — function or method definition (the name node)
;   @def.class     — class definition (the name node)
;   @import.path   — string literal that is an import/require path
;   @call.name     — function call name (for call-graph edges)

; --- Function declarations ---
(function_declaration
  name: (identifier) @def.function)

(function_expression
  name: (identifier) @def.function)

(arrow_function) @def.function

; --- Method definitions ---
(method_definition
  name: (property_identifier) @def.function)

; --- Class declarations ---
(class_declaration
  name: (type_identifier) @def.class)

(class_expression
  name: (type_identifier) @def.class)

; --- Interface and type alias declarations ---
(interface_declaration
  name: (type_identifier) @def.class)

(type_alias_declaration
  name: (type_identifier) @def.class)

; --- ES module imports ---
(import_statement
  source: (string
    (string_fragment) @import.path))

; --- Dynamic import() ---
(call_expression
  function: (import)
  arguments: (arguments
    (string
      (string_fragment) @import.path)))

; --- require() ---
(call_expression
  function: (identifier) @_req
  arguments: (arguments
    (string
      (string_fragment) @import.path))
  (#eq? @_req "require"))

; --- Export re-exports ---
(export_statement
  source: (string
    (string_fragment) @import.path))

; --- Function calls (for call-graph edges) ---
(call_expression
  function: (identifier) @call.name)

(call_expression
  function: (member_expression
    property: (property_identifier) @call.name))
