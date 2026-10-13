// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefile

import (
	"io"

	"github.com/vmvarela/ghoten/internal/encryption"
	tfversion "github.com/vmvarela/ghoten/version"
)

// Write escribe el estado en el formato de serialización actual, como JSON
// compacto con un único salto de línea final.
func Write(s *File, w io.Writer, enc encryption.StateEncryption) error {
	// Registra siempre la versión actual de Ghoten en el estado.
	s.TerraformVersion = tfversion.SemVer

	diags := writeStateV4(s, w, enc, false)
	return diags.Err()
}

// WriteIndent escribe el estado en el formato de serialización actual, con
// sangría de dos espacios y un único salto de línea final.
//
// La sangría se aplica al texto en claro antes del cifrado, por lo que el
// resultado cifrado sigue siendo un sobre compacto. Se usa para archivos
// locales; los sistemas remotos deben usar Write.
func WriteIndent(s *File, w io.Writer, enc encryption.StateEncryption) error {
	// Registra siempre la versión actual de Ghoten en el estado.
	s.TerraformVersion = tfversion.SemVer

	diags := writeStateV4(s, w, enc, true)
	return diags.Err()
}

// WriteForTest escribe el estado con el formato indentado de WriteIndent sin
// reemplazar su versión. Se usa en pruebas que necesitan especificar otra
// versión.
func WriteForTest(s *File, w io.Writer) error {
	diags := writeStateV4(s, w, encryption.StateEncryptionDisabled(), true)
	return diags.Err()
}
