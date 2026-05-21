; queries/java/symbols.scm
;
; Tree-sitter query for Java symbol extraction.

; --- Method declarations ---
(method_declaration
  name: (identifier) @def.function)

(constructor_declaration
  name: (identifier) @def.function)

; --- Class, interface, enum declarations ---
(class_declaration
  name: (identifier) @def.class)

(interface_declaration
  name: (identifier) @def.class)

(enum_declaration
  name: (identifier) @def.class)

(record_declaration
  name: (identifier) @def.class)

; --- Import declarations ---
(import_declaration
  (scoped_identifier) @import.path)

; --- Method invocations ---
(method_invocation
  name: (identifier) @call.name)
