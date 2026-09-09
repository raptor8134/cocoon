// Package wind - complete program assembly.
//
// A generated file should be self-describing. Slicers that embed their settings
// in the output are the model here: months later the G-code is often the only
// surviving artifact, and being able to recover exactly what produced it is
// worth the bytes.
package wind

import (
	"fmt"
	"strings"
	"time"
)

// ProgramInfo carries the provenance embedded in a program's header.
type ProgramInfo struct {
	// SourceName is the wind file's name, e.g. "nosecone.json".
	SourceName string

	// SourceJSON is the full configuration, embedded verbatim as comments so
	// the program can be reproduced from the G-code alone.
	SourceJSON string

	// ProfileName and ProfileCSV embed the mandrel profile when one was used.
	// Without it the embedded config references a file that may since have
	// changed, so the record would be incomplete.
	ProfileName string
	ProfileCSV  string

	// Generated is the timestamp written into the header. Zero means now.
	Generated time.Time

	// Density is the fiber density in g/cm^3, used for the mass estimate.
	// Zero omits mass. E-glass is about 2.55, carbon about 1.75.
	Density float64
}

// comment prefixes a line as a G-code comment. FormatGcodeLines appends the
// trailing semicolon, so nothing is added here.
func comment(format string, args ...any) string {
	return "; " + fmt.Sprintf(format, args...)
}

// BuildProgram assembles header, body and footer into one program.
//
// The header carries provenance and estimates; the body is the motion; the
// footer carries the bulkier per-layer detail and the stop commands. Both are
// generated from the model rather than stored as fixed text, so they cannot
// drift out of step with what was actually produced.
func BuildProgram(w *Wind, info ProgramInfo) []string {
	when := info.Generated
	if when.IsZero() {
		when = time.Now()
	}
	metrics := ComputeMetrics(w.Layers, w.Machine)

	var out []string
	out = append(out, header(w, info, metrics, when)...)
	out = append(out, w.StartGcode...)
	out = append(out, Layers2Gcode(w.Layers, w.Machine)...)
	out = append(out, w.EndGcode...)
	out = append(out, footer(w, info, metrics)...)
	return out
}

func header(w *Wind, info ProgramInfo, m WindMetrics, when time.Time) []string {
	ax := w.Machine.Axes.withDefaults()
	name := info.SourceName
	if name == "" {
		name = "(unsaved)"
	}

	out := []string{
		comment("======================================================"),
		comment(" Cocoon - CNC Operated COmposite Overwrap Navigator"),
		comment("======================================================"),
		comment(" source     : %s", name),
		comment(" generated  : %s", when.Format("2006-01-02 15:04:05 MST")),
		comment(""),
		comment(" ESTIMATES"),
		comment("   run time : %s", FormatDuration(m.Seconds)),
		comment("   tow used : %.2f m", m.TowLength/1000),
	}
	if mass := m.TowMass(w.Filament, info.Density); mass > 0 {
		out = append(out, comment("   tow mass : %.1f g (at %.2f g/cm3)", mass, info.Density))
	}
	out = append(out,
		comment("   moves    : %d", m.Moves),
		comment(""),
		comment(" MACHINE"),
		comment("   axes     : carriage=%s radial=%s eye=%s spindle=%s",
			ax.Carriage, ax.Radial, ax.Eye, ax.Spindle),
		comment("   spindle  : %s", spindleWord(w.Machine)),
		comment("   feed     : %s at %.1f mm/s surface speed",
			feedWord(w.Machine), surfaceSpeed(w.Machine)),
	)

	// The shortest move is worth surfacing: if it is very brief, acceleration
	// dominates and the controller will not achieve the commanded duration.
	if m.MinSeconds > 0 && m.MinSeconds < 0.02 {
		out = append(out, comment(""),
			comment("   WARNING: shortest move is %.4f s. Acceleration may", m.MinSeconds),
			comment("   dominate, so commanded timings may not be met."))
	}

	out = append(out, comment(""), comment(" CONFIGURATION (verbatim, for reproducing this program)"))
	out = append(out, embedBlock("wind", info.SourceJSON)...)

	if info.ProfileCSV != "" {
		out = append(out, comment(""),
			comment(" MANDREL PROFILE: %s", info.ProfileName))
		out = append(out, embedBlock("profile", info.ProfileCSV)...)
	}

	out = append(out, comment("======================================================"), "")
	return out
}

func footer(w *Wind, info ProgramInfo, m WindMetrics) []string {
	out := []string{
		"",
		comment("======================================================"),
		comment(" LAYER DETAIL"),
	}
	for i := range w.Layers {
		l := &w.Layers[i]
		if l.Disabled {
			out = append(out, comment("   %2d. %-8s DISABLED (repeat 0)", i+1, l.LType))
			continue
		}
		switch l.LType {
		case "hoop":
			out = append(out, comment("   %2d. %-8s stepover %.2f mm, repeat %d, %d pts, %.1f deg",
				i+1, l.LType, l.Params.Stepover, l.Repeat, len(l.FullPath), l.DAOuter))
		default:
			out = append(out, comment("   %2d. %-8s angle %.1f deg, repeat %d, %d pts, %.1f deg",
				i+1, l.LType, l.Params.Angle, l.Repeat, len(l.FullPath), l.DAOuter))
		}
	}

	// Coverage summary, so the operator can see whether the layup was even
	// without opening the app.
	if w.Mandrel != nil {
		cov := NewCoverage(w.Mandrel, 200, 180)
		for i := range w.Layers {
			if !w.Layers[i].Disabled {
				cov.AddPath(w.Layers[i].FullPath, w.Filament)
			}
		}
		rep := cov.Report(w.Filament)
		var maxT, sumT float64
		gaps := 0
		for _, st := range rep {
			sumT += st.Thickness
			if st.Thickness > maxT {
				maxT = st.Thickness
			}
			if st.Gap {
				gaps++
			}
		}
		out = append(out, comment(""), comment(" COVERAGE"),
			comment("   mean thickness : %.3f mm", sumT/float64(len(rep))),
			comment("   peak thickness : %.3f mm", maxT),
			comment("   fiber laid     : %.2f m", m.TowLength/1000))
		if gaps > 0 {
			out = append(out, comment("   WARNING: %d of %d stations have an uncovered sector",
				gaps, len(rep)))
		}
	}

	out = append(out,
		comment("======================================================"),
		"M5 ; spindle stop",
		"M2 ; end of program",
	)
	return out
}

// embedBlock renders arbitrary text as commented lines.
//
// Every line is prefixed so the block cannot terminate early on content that
// happens to contain a newline or a semicolon.
func embedBlock(tag, body string) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	out := []string{comment(" >>> begin %s", tag)}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		out = append(out, comment("   %s", line))
	}
	out = append(out, comment(" <<< end %s", tag))
	return out
}

func spindleWord(m Machine) string {
	if m.SpindleDirection == SpindleReverse {
		return "reverse (negated)"
	}
	return "forward"
}

func feedWord(m Machine) string {
	if m.FeedMode == FeedUnitsPerMinute {
		return "G94 units/min"
	}
	return "G93 inverse time"
}

func surfaceSpeed(m Machine) float64 {
	if m.SurfaceSpeed > 0 {
		return m.SurfaceSpeed
	}
	return 100
}
