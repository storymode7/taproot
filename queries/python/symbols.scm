; queries/python/symbols.scm
;
; Tree-sitter query for Python symbol extraction.

; --- Function and async function definitions ---
(function_definition
  name: (identifier) @def.function)

(decorated_definition
  definition: (function_definition
    name: (identifier) @def.function))

; --- Class definitions ---
(class_definition
  name: (identifier) @def.class)

; --- Imports ---
; import foo.bar
(import_statement
  name: (dotted_name) @import.path)

; from foo.bar import baz
(import_from_statement
  module_name: (dotted_name) @import.path)

; from . import foo  (relative)
(import_from_statement
  module_name: (relative_import) @import.path)

; --- Function calls ---
(call
  function: (identifier) @call.name)

(call
  function: (attribute
    attribute: (identifier) @call.name))
