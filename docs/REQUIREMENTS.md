# Cocoon — Requirements

> **Status: draft, review in progress.** Sections 1–5 incorporate your review
> of 2026-09-08. Remaining **[CONFIRM]** markers are still guesses that affect
> generated G-code. Edit freely.
>
> Last updated: 2026-09-08

---

## 1. What this is

Cocoon generates tool paths and G-code for a **CNC filament winding machine** —
the kind that wraps continuous fiber around a rotating mandrel to lay up
composite parts.

You describe a wind (mandrel shape, filament, a stack of layers), and Cocoon
produces the machine program plus a 3D preview of the fiber path.

It is a Go port of the path-generation logic from an earlier Python project
(`gcode_gen`).

**Audience:** initially a small team. A public release is intended, but
**primarily as a demonstration** rather than to serve a large user base.

This shapes priorities: error messages and the 3D preview should be good
enough to show the tool off and to onboard a teammate, but exhaustive browser
support, i18n, accessibility audits, and hardening against hostile input are
not warranted. A public demo means it should not embarrass you on first
contact — it does not mean it must survive arbitrary users.

---

## 2. Scope

### In scope

- Turning a wind configuration into G-code for a filament winder.
- Visualising the resulting fiber path on the mandrel in 3D.
- **Checking the path against fiber slip** (see §4).
- **Reporting coverage** along the mandrel (see §4).
- Running on the web, Windows, and Linux, with a clean GUI. macOS is a
  maybe-nice-to-have.
- Loading and saving wind configurations as local files, easily, on every
  platform.
- Emitting G-code for **more than one controller flavor**, selectable in
  settings.
- **Accepting mandrel geometry from the CAD tools people already use** (§10).
  Most mandrel geometry exists in CAD before Cocoon sees it, so this is the
  largest usability lever available.

### Out of scope

- **Process simulation** — cure, resin content, fiber tension dynamics.
  Confirmed out for now.
- Structural analysis of the layup (fiber angle vs. load case).
- Machine control — Cocoon produces a file; something else runs it.
- Multi-mandrel or multi-spindle machines.
- Non-axisymmetric mandrels.

---

## 3. The machine

### Axis naming: survey result

**There is no standard letter assignment for filament winders.** Machines are
described by *function* — mandrel rotation, carriage, cross-feed, eye rotation
— and vendors disagree on letters. X-Winder, for example, labels the spindle
"Axis 1 or X" and the carriage "Y", which is the opposite of lathe convention.
Six-axis machines are typically described as "3 linear and 3 rotary" with no
canonical lettering at all.

Since no standard exists, **we adopt the lathe convention (ISO 841)** as the
default, per your instruction:

| Function | Lathe convention | Cocoon emits today | Change? |
|---|---|---|---|
| Carriage — along mandrel axis | **Z** | `X` | yes |
| Cross-feed — radial | **X** | `Z` | yes |
| Mandrel rotation | **C** (about Z) | `A` | yes |
| Payout eye rotation | **A** (about X) | `Y` | yes |

Rationale: on a lathe, `Z` is parallel to the spindle axis, `X` is the radial
cross-slide, and rotations `A`/`B`/`C` are about `X`/`Y`/`Z` respectively — so
spindle rotation is `C`. A winder is geometrically a lathe with a payout eye
instead of a tool.

**DECIDED and IMPLEMENTED (2026-09-08).** Lathe lettering is now the default
output, with every letter aliasable in Settings.

The orientation follows from wanting forward rotation to be **+C**:

| Axis | Direction | Notes |
|---|---|---|
| **X** | horizontal, front-facing | radial cross-feed; matches lathe +X toward the operator |
| **Y** | vertical, up | completes the right-handed frame; nothing is emitted on it |
| **Z** | along the mandrel | **+Z points opposite the direction the carriage advances** |
| **A** | rotation about X | payout eye |
| **C** | rotation about Z | mandrel; forward spin is +C |

> +C about +Z rotates X toward Y (right-hand rule). Forward sweeps the mandrel
> surface from front to up, so **X = front** and **Y = up**, which makes
> **Z = X × Y point opposite the direction the carriage advances**.

So the carriage travels in **−Z**. That is a real consequence, not a choice,
and the 3D view draws it that way rather than hiding it. If travelling in −Z is
unacceptable, the alternatives are to accept negative C, or to set
`spindle_direction: "reverse"` and re-orient — the configuration now absorbs
either.

`Y` is drawn but nothing is emitted on it: the machine has no second linear
radial axis. It exists so the frame's handedness is checkable by eye.

**Aliasing.** `machine.axes` accepts `carriage`, `radial`, `eye`, `spindle`,
each a single letter, defaulting to `Z`/`X`/`A`/`C`. Unset entries fall back
individually, so one axis can be aliased without restating the rest. Invalid
values are rejected rather than emitted. The Settings tab edits these live and
the viewer labels track them.

#### The direction question

Your spin is left-handed about the carriage axis as currently oriented, which
means it cannot be expressed as a positive `C`. Two ways out:

**Option A — point the carriage axis the other way** (left instead of right,
viewed from the front). Flipping the axis flips the handedness of rotation
about it, so the same physical spin becomes a positive `C` and the frame stays
right-handed. Costs: it inverts what a lathe looks like, which is
counter-intuitive; and you have said you do not want the carriage origin at the
left running rightward.

**Option B — leave the axis alone and allow negative `C`.** Nothing is
mathematically wrong with a negative rotary command; it is only aesthetically
unsatisfying.

**Option C — make the direction configurable.** IMPLEMENTED. This is the one
that survives contact with reality: whether "forward" pulls the filament up
over the top or down under the bottom depends on which side of the carriage the
payout eye sits on, and that **varies between machines**. Baking either
handedness into the generator would silently produce mirrored parts on a
machine built the other way round.

`machine.spindle_direction` is `"forward"` (default, unchanged behaviour) or
`"reverse"`, which negates emitted spindle angles. It is exposed in the block
editor and reflected in the viewer's rotation arrow, so what you see is what
the machine will do. Being a machine property, it is the first field of the
machine profile that the flavor work will extend.

Note this makes Option A and Option B largely equivalent in practice — pick the
axis orientation you find most readable, and let the configuration absorb the
handedness.

### Flavor-agnostic internal representation

Per your instruction, the generator must **not** emit letters directly.

- Path generation produces moves in **functional terms**: carriage position,
  radial position, spindle angle, eye angle, feed.
- A **machine profile** (part of settings) maps those to axis letters, decides
  the G-code dialect, and supplies header/footer.
- Emitters are per-flavor and are the only place that knows about `G0`/`G1`
  spelling, `M117` vs. alternatives, wrapping behaviour, and number formatting.

This makes the axis change above a *profile* change rather than a code change,
and it is what lets `M117` (Marlin-specific) stop being hardcoded.

**[CONFIRM] Which flavors matter first?** Candidates: Marlin (the current
output looks Marlin-shaped), GRBL, LinuxCNC, Duet/RepRapFirmware, generic
ISO 6983. Naming the first two decides what the profile abstraction has to
cover.

### Feedrate: use G93 inverse-time

Your problem — "the firmware assumed old XYZ axes and counted the angle axis as
an extruder" — is the standard one, and it has a standard answer.

Under normal feed mode (G94), a controller has to combine linear mm and rotary
degrees into one feedrate, and there is no correct way to do it. LinuxCNC's own
rule is telling: *if any linear axis is moving, only the linear axes are used
in the feedrate calculation; otherwise the rotary axes are.* Different
controllers make different arbitrary choices, which is why the same file
behaves differently on each.

**G93 inverse-time mode sidesteps it entirely.** In G93, `F` means *this move
takes 1/F minutes*. The controller is told the duration, not a speed, so it
never has to reconcile units. That gives us exactly what you asked for:

- Cocoon computes the surface speed it wants from the XZC motion, ignoring the
  eye axis.
- It converts that to a move duration.
- It emits `F = 1 / duration_minutes`.

The controller then hits that duration regardless of how it would otherwise
weigh linear against rotary. **Varying feedrate to reduce slip becomes trivial**
— it is just a different duration per move, computed from local conditions.

Requirements:

- **FR1** Represent feed internally as a **target surface speed** (mm/s of tow
  laid), not as a raw `F` number.
- **FR2** Compute per-move duration from the XZC motion; the eye axis does not
  participate.
- **FR3** Emit G93 inverse-time for flavors that support it.
- **FR4** For flavors without G93, the emitter must say so rather than
  silently producing a file that runs at the wrong speed.
- **FR5** Allow feed to vary along the path, so it can be reduced where slip
  risk is high (depends on the slip work in §4.1).

#### Does G93 actually move everything together?

**Yes.** `G1` is *coordinated* motion by definition: every axis in the move
interpolates simultaneously and they all arrive together. G93 only sets how
long that coordinated move takes. Linear and rotary axes are not scheduled
separately — the rotary axis reaches its endpoint at the same instant the
linear ones do.

**The caveat that matters for us:** the controller honours the commanded time
only if it *can*. If the duration would require any axis to exceed its maximum
velocity, or if acceleration limits cannot reach the required speed within the
move, the planner slows the move down and the timing is silently lost. So:

- **FR6** Cocoon must know per-axis maximum velocities (part of the machine
  profile) and **clamp** each commanded duration so no axis is over-driven.
  Without this, "exact move time" is an assumption rather than a guarantee.
- **FR7** Very short moves are dominated by acceleration and blending. Path
  resolution (`res = 5 mm`) should be chosen with that in mind.

#### Controller options

| Controller | G93 | Needs a PC | Cost | Notes |
|---|---|---|---|---|
| **LinuxCNC** | full | yes (or Pi) | free | Best support; the reference target |
| grblHAL | partial | no | free | 32-bit GRBL, 4+ axes; community rotary post-processors tested at **constant feed only** |
| RepRapFirmware / Duet | **no** | no | board cost | G93 requested upstream, not implemented; same linear/rotary rule as Marlin |
| FluidNC (ESP32) | no | no | free | Rotary yes, inverse-time no |
| Klipper | no | yes (Pi) | free | 3D-printer oriented |
| Machinekit | yes | no (BeagleBone) | free | LinuxCNC fork for ARM; less active now |
| MASSO | yes | **no** | ~$1–2k | Standalone controller, but commercial |

#### LinuxCNC on a Raspberry Pi

Viable, with one hardware caveat. LinuxCNC 2.9.2 runs on Pi under Debian
Bookworm (uspace + PREEMPT\_RT). But:

- **Pi 5's RP1 chip broke the Mesa SPI driver** — SPI does not work there.
- **GPIO step generation is jittery** on any Pi and is not recommended for
  reliable stepping.
- **Use a Mesa *Ethernet* card** (7i92, 7i96 and similar). Ethernet sidesteps
  the SPI problem entirely, works on Pi 4 and Pi 5, and does not tie you to one
  host machine.

So the realistic free setup is **Pi 4 or 5 + Mesa Ethernet card**, with the
Mesa board doing step generation and the Pi running the planner.

#### The G94 fallback is better than it looks

If G93 is unavailable, we are not stuck. The rule Duet and LinuxCNC both
document — *if any linear axis is moving, only the linear axes are used in the
feedrate calculation; otherwise the rotary axes are* — is deterministic, so a
flavor emitter that knows it can compute a correct `F`:

- moves with linear content: `F = linear_distance / duration`
- pure-rotary moves (a hoop pass at constant carriage position): `F = degrees / duration`

That is exactly the sort of controller-specific knowledge the flavor
abstraction exists to hold. Marlin remains the odd one out, because it
misclassified the rotary axis as an extruder rather than applying a documented
rule.

**[CONFIRM]** Target LinuxCNC first?

### Remaining machine questions **[CONFIRM]**

1. **Is the payout eye a rotary axis in degrees?** The code computes it as
   `90 - angle`, which reads as an orientation, not a position.
2. **Does the spindle angle need wrapping?** Angles currently accumulate
   without bound — 30,000°+ in a normal wind. Some controllers accept that;
   others lose precision or reject it. If wrapping is needed it belongs in the
   emitter, but whether it's *safe* to wrap depends on how the controller
   handles a rotary rollover mid-program.
3. **Is the radial axis actually commanded,** or is it informational and the
   machine infers it from the mandrel profile?
3b. **Where is the carriage origin?** You have said you do not want to start at
   the left and travel right, which constrains the axis-orientation choice
   above independently of handedness.
4. **Homing / parking** — what should the header and footer do?
5. **Units** — millimetres and degrees throughout. Confirm.

---

## 4. Domain rules

### Layer types

- **Hoop** — near-circumferential winding, effectively 90° to the axis. The
  carriage steps along by `stepover` mm per revolution.
- **Helical** — winds at `angle` degrees measured **from the mandrel axis**,
  traversing back and forth along the mandrel.

### Currently implemented

- The mandrel rotates **continuously in one direction**; the carriage traverses
  back and forth. Spindle angle only ever increases.
- On a helical layer's **return pass**, the payout eye angle is negated (the
  eye mirrors), but spindle rotation does not reverse.
- Layers wind **continuously**: each layer starts at the angle the previous
  one ended, so there is no rotational discontinuity between layers.
- A helical layer computes an **inner repeat count** — how many circuits are
  needed for the filament band to cover the mandrel circumference — from the
  filament width and the widest mandrel radius.
- The **end turnaround angle** is the smallest value that both exceeds
  `180 - 2 × angle` and is coprime with the inner repeat count, so successive
  circuits don't retrace each other.

---

### 4.1 Fiber slip — to be expanded

You flagged the `180 - 2 × angle` turnaround minimum as "quick and dirty slip
prevention". Agreed, and the literature gives us the proper model.

**The standard framework:**

- A **geodesic** path is the shortest route across the surface and needs **no
  friction to stay put**. On a surface of revolution it satisfies the
  **Clairaut relation**: `r · sin(α) = constant`, where `α` is the winding
  angle. A geodesic naturally turns around where `r` equals that constant —
  the polar opening.
- Any deviation from geodesic is a **non-geodesic** path, held in place only
  by friction. It is characterised by the **slippage coefficient** `λ`, the
  ratio of lateral to normal force on the tow. The path is stable while
  `λ ≤ μ` (the static friction coefficient between tow and mandrel).
- Setting `λ = 0` degenerates to the Clairaut relation, so geodesic is just
  the zero-friction special case of the same equations.
- Measured slippage coefficients in the literature run roughly **0.2 – 0.39**,
  material- and condition-dependent (wet vs. dry, resin, surface).
- **Return regions are inherently non-geodesic** — the turnaround at the end of
  a stroke cannot be geodesic — which is exactly where the current heuristic
  applies.

**How much does geodesic differ? Computed for your actual profile.**

Clairaut says a wind starting at angle `a0` on radius `R` can only reach down
to `r0 = R * sin(a0)` before it must turn around. For the
`4in_3to1_tanogive` profile (R = 50 mm at the body, 15.58 mm at the tip):

| Requested at body | Geodesic angle at r=44.5 | at r=37.2 | Turns around at | Reaches tip? |
|---|---|---|---|---|
| 30° | 34.2° | 42.2° | r = 25.0 mm | no |
| 45° | 52.7° | 71.8° | r = 35.4 mm | no |
| 60° | 76.9° | unreachable | r = 43.3 mm | no |

So the answer to "how much does it change the angle" is: **nothing on a
cylinder, and a great deal on a taper** — the geodesic angle at 45° has climbed
to 52.7° only 100 mm along, and by 150 mm the path has already had to turn
around. Cocoon meanwhile marches on at a flat 45° all the way to the tip.

**Which curvatures force deviation:** any radius reduction at all. The useful
number is that a geodesic reaching a tip of radius `r_tip` from a body of
radius `R` cannot exceed `arcsin(r_tip / R)`. For this profile that is
**arcsin(15.58/50) = 18.2°**.

**This is much less limiting than it first looks**, because (confirmed) a
nosecone does not need winding all the way to the tip. Turning around short of
the tip is the *normal* geodesic behaviour, not a failure: a 45° wind naturally
reverses at r = 35.4 mm, and that turnaround radius is a design output rather
than an error. The practical rule becomes:

> Choose the winding angle to place the turnaround where you want coverage to
> end. `r_turnaround = R * sin(a)`.

So the requirement is not "make steep angles reach the tip" but **"report where
each layer's geodesic turnaround falls, and let coverage stop there"**. A
separate low-angle layer (≤18° here) can cover the tip if it needs covering.

**A finding worth acting on:** Cocoon currently holds the winding angle
**constant** along the whole traverse. On a cylinder that is geodesic (`r` is
constant, so constant `α` satisfies Clairaut trivially). **On any tapered
profile it is not.** Your default `test.json` uses the `4in_3to1_tanogive`
profile, so the generated path is already non-geodesic along the entire ogive,
with no friction check anywhere. Whether that slips in practice depends on `μ`
and how sharply the profile tapers — but right now nothing tells you.

**Requirements:**

- **SL1** Compute the slippage coefficient `λ` along the generated path.
- **SL2** Warn when `λ` exceeds the configured `μ`, identifying *where* on the
  mandrel (axial position) and *which layer*.
- **SL3** Make `μ` configurable — it is a material/process property, so it
  belongs in settings or in the filament definition, not hardcoded.
- **SL4** Replace the `180 - 2 × angle` turnaround heuristic with a
  `λ`-based turnaround, once SL1 exists.
- **SL5** Offer a **geodesic mode** that derives `α(x)` from Clairaut
  (`α = arcsin(r₀ / r)`) instead of holding `α` constant, so a wind can be
  made friction-independent by construction.
- **SL6** Report each helical layer's **geodesic turnaround radius and axial
  position**, so the coverage extent of a given angle is visible before
  generating. Cheap to compute (`R*sin(a)` against the profile) and useful even
  without full slip checking.

**[CONFIRM]:**
- Do you want geodesic paths as the default, with constant-angle as an
  override? Or keep constant-angle default and just warn?
- What `μ` do you actually see with your fiber/resin/mandrel combination?
- Should exceeding `μ` be a hard error (refuse to generate) or a warning?

---

### 4.1b Winding angle accuracy — TWO BUGS, both FIXED

`GenPointsHelical` had two independent errors in its angular advance.

**1. Radians substituted for a tangent.** For a tow at angle `a` advancing `dx`
axially, the circumferential arc is `dx*tan(a)`. The code used the angle in
radians where `tan(angle)` belongs. Since `tan(a) ≈ a` only for small `a`, the
error grew with angle: 45° laid 37.9°, 60° laid 45.9°, 80° laid 53.7°.

**2. A spurious arctangent.** The advance was wrapped in `atan2`, which treats
the step as a chord through the solid. The tow lies ON the surface, so
`r*dTheta = dx*tan(a)` exactly, with no arctangent. This cost a further 0.4° at
45° and 3.3° at 80°, and got worse with coarser steps.

Both fixed. Verified by measuring the laid angle back out of generated paths:

| Requested | Was | Now |
|---|---|---|
| 30° | 27.55° | 30.0000° |
| 45° | 37.92° | 45.0000° |
| 60° | 45.91° | 60.0000° |
| 80° | 53.70° | 80.0000° |

Exact at every angle. **Existing saved configs wind differently now** — if any
were tuned by eye against the old behaviour, the numbers in them are ~7° low at
45° and should be revisited.

### 4.2 Coverage reporting — IMPLEMENTED

Implemented in `internal/wind/coverage.go`, surfaced as a Coverage tab.

**Method — volume conservation.** A tow of width `w` and thickness `t` laid
along `ds` deposits `w*t*ds` of material, spread over the mandrel patch it
covers. Deposition is accumulated on a 2D grid: 200 axial stations by 180
angular sectors (2° each). The angular dimension is what separates "covered"
from "thick on average" — a layer can deposit plenty at a station and still
leave bare stripes between passes, and averaging over theta hides exactly that.

The tow is treated as a **band, not a line**. At angle `a` from the axis its
width projects as `w*sin(a)` axially and `w*cos(a)` circumferentially, so a
hoop pass is `w` wide along the axis and an axial pass is not. Getting this
wrong concentrates a 20 mm tow into one 0.5 mm station and reports tens of
millimetres of thickness on a part that has under one.

**Validated against closed form:** a hoop layer with stepover equal to tow
width must be exactly one tow thick. Measured 0.258 mm against 0.250 mm
expected (~3% high from band-edge discretization); halving the stepover gives
0.508 mm, correctly doubling.

- **CV1** ✅ Thickness plotted against axial position, with a mean reference
  line so bulges and dips read against it.
- **CV2** ✅ Falls out of the method — convergence near turnarounds shows up
  as a peak without special-casing. On the default config the end bulges reach
  2.2× the mid-span thickness.
- **CV3** ✅ Stations with any bare sector are flagged, marked on the plot, and
  counted in a warning. **[CONFIRM]** I read "gaps along each dz radius" as:
  at a station, walk the circumference and check whether bands tile it or
  leave bare stripes. That is what is implemented.
- **CV4** ✅ Per-station thickness is the plotted quantity.
- **CV5** Feed thickness back into the mandrel radius so each layer winds on
  the built-up surface. `Coverage.GrownMandrel` computes it; **not yet wired
  into generation**, because doing so changes every multi-layer path.
  **[CONFIRM]** to enable.

**Tiling vs. inner repeat.** You suspected tiling belongs in the inner-repeat
logic rather than as a separate check. Agreed — inner repeat already derives
circuit count from tow width, so it is the mechanism; the coverage grid is the
independent *validation* that the mechanism worked. Keeping them separate is
deliberate: a check that shares its assumptions with the thing it checks cannot
catch that the assumptions are wrong.

---

### 4.3 Rules not implemented

- **Layer thickness is ignored.** `Filament.Thickness` is parsed but nothing
  accumulates radius between layers, so layer 5 winds at the bare mandrel
  radius. This blocks CV1/CV4 and is now on the critical path for coverage
  reporting. **[CONFIRM]** should each layer build radius by its thickness,
  scaled by local coverage?

---

### 4.4 Program header and footer — to be designed

Currently `StartGcode`/`EndGcode` are unused. They should carry provenance, in
the spirit of 3D slicers embedding their settings.

**Header:**
- **HD1** Generation timestamp and source filename.
- **HD2** The **full `wind.json` embedded as comments**, so a G-code file alone
  is enough to reconstruct exactly the settings that produced it.
- **HD3** Optionally the CSV profile too. **[CONFIRM]** — the profile is what
  makes the embed actually complete; without it the config references a file
  that may have changed. Size cost is real but small next to the path itself.
- **HD4** Filament usage estimate (length, mass if density is known) and
  estimated run time, the way a slicer reports them.

**Footer:**
- **FT1** Bulkier metadata — per-layer statistics, coverage summary, slip
  warnings.
- **FT2** Cleanup commands common to all flavors (spindle stop, park).

**Design note:** these should be *generated from the model*, not stored as
semi-hardcoded text blocks. The flavor emitter contributes the machine-specific
commands; Cocoon contributes the metadata. That keeps provenance identical
across flavors and stops the header drifting out of sync with what was
generated.

Filament usage (HD4) needs the same machinery as the progress markers: a
per-move record of tow length and duration. `Coverage.FiberLength` already
provides total length; duration needs FR1–FR2.

**[CONFIRM]** Fiber density / areal weight, so usage can be reported as mass?

## 5. Functional requirements

### Must

- **M1** Generate G-code from a wind configuration.
- **M2** Support cylindrical mandrels (length + diameter) and arbitrary
  axisymmetric profiles (CSV of X/radius points).
- **M3** Support hoop and helical layers, stackable in any order, each with a
  repeat count.
- **M4** Show the generated fiber path in 3D on the mandrel, updating live as
  the configuration changes.
- **M5** Reject invalid configurations with a message naming the offending
  parameter — never hang, never emit silently-wrong G-code.
- **M6** Run in the browser, on Windows, and on Linux.
- **M7** Open and save configuration files locally on each platform.
- **M8** Never lose in-progress work to a crash, reload, or tab close.
- **M9** Represent moves in a **flavor-agnostic internal form**; emit letters
  and dialect only at the final stage.
- **M10** Let the user select the **G-code flavor / machine profile** in
  settings.

### Should

- **S1** Edit the layer stack visually (blocks/dropdowns) without touching JSON.
- **S2** Keep a raw JSON view as a toggle, for power editing and diffing.
- **S3** Accept JSON5 (comments, unquoted keys, trailing commas) in configs.
- **S4** Work offline once loaded.
- **S5** Export G-code as a file.
- **S6** Apply filament feedrate to the output. *(Not yet done — §6.)*
- **S7** Slip checking — **SL1–SL4** above.
- **S8** Coverage reporting — **CV1–CV4** above.
- **S9** Model layer thickness / radial buildup (prerequisite for S8).

### Later

- **L0** Mandrel geometry sources: parametric curves and CAD import (§10).
- **L1** Geodesic winding mode (**SL5**).
- **L2** A "bucket": browser-persistent storage holding wind configs, CSV
  profiles, and a global `settings.json`, with individual download and a
  whole-system export/import.
- **L3** Unified file dialog with the same UX on desktop and web.
- **L4** Native OS file dialogs on desktop.
- **L5** Filament presets in a global settings file (currently hardcoded).
- **L6** Automated tests for the winding core.
- **L7** Absolute rotation between layers (`AbsRot`) — "for future bolted COPV".

### Won't (for now)

- Process simulation (confirmed out).
- Native desktop app shell. The PWA covers Windows/Linux/macOS from one
  codebase.
- Cloud sync, accounts, or multi-user collaboration.
- Mobile-first UI. Should not break on a tablet; phones are not a target.
- Extensive browser-compatibility work beyond Chromium + a graceful message
  elsewhere — justified by the small, known audience.

---

## 6. Known gaps

| Gap | Effect | Where |
|-----|--------|-------|
| Feedrate ignored | Every line emits `F1000` regardless of config | `TODO(feedrate)` in `internal/wind/gcode.go` |
| Progress markers positional, not time-based | `M117` percentages track point index, not elapsed time | same |
| Layer thickness ignored | Layers don't build up radius; **blocks coverage reporting** | §4.3 |
| No slip check | Non-geodesic paths on tapered mandrels are unverified | §4.1 |
| Axis letters hardcoded | Blocks multi-flavor output | §3 |
| `StartGcode`/`EndGcode` unused | No header/footer emitted | `internal/wind/types.go` |
| CSV profiles filesystem-bound in CLI | Web build serves them from `web/profiles/` | `NewMandrelFromCSV` |
| No tests | — | — |

---

## 7. Assumptions I made — please correct

Live guesses baked into current output.

1. ~~`DAOuter`~~ **CONFIRMED**: it is the layer's total angular span, so the
   spindle can be driven in **absolute** mode. (Relative mode caused problems
   with the 3D printer firmware.) This means the emitter must never wrap the
   accumulated angle without also rewriting the absolute reference — see the
   wrapping question in §3.
2. **`DAInner`** is one one-way pass for hoop, the averaged per-circuit advance
   for helical. Nothing reads it today.
3. ~~First move is a rapid~~ **RESOLVED — no rapids at all.** The operator
   preps a wind by hand (a few turns at the Z=0 end to snug the tow), so the
   machine is already at the start with fiber under tension. A rapid would
   either slew away from that carefully set position or drag the tow at
   maximum axis rate. Every emitted move is now `G1`.
4. ~~Progress markers~~ **CONFIRMED** as `min(100, points/10)`. Now that
   per-move durations exist (FR1/FR2), they can key off elapsed time; still
   positional pending that switch.
5. ~~Eye angle negation~~ **CONFIRMED** correct, and the spindle only ever runs
   forwards. Forward is **left-handed about the mandrel axis**, so the filament
   is pulled upwards over the top of the mandrel. That handedness has to be
   preserved through the axis remap in §3 — a naive letter swap can silently
   mirror it.
6. ~~`nrepeat: 0`~~ **CONFIRMED and IMPLEMENTED**: it disables the layer. The
   layer is kept with its parameters intact, contributes nothing to the path or
   G-code, and does not advance the running angle — so muting one leaves every
   other layer's output byte-identical. The block editor greys it out and shows
   an "off" badge.
7. ~~Filament width/thickness~~ **CONFIRMED**: width is the band width laid
   down (used for coverage), thickness is radial buildup.

---

## 8. Decisions made

- **2026-09-08 — STEP is the primary CAD import route.** A revolved mandrel is
  carried in analytic surface entities that state axis and radius explicitly,
  so the profile is read rather than inferred from a mesh or a 2D sketch.
  Crucially, this needs the whole simple-surface family: a cylinder is a
  `CYLINDRICAL_SURFACE`, *not* a `SURFACE_OF_REVOLUTION`, so a parser looking
  only for the latter would miss the body of every rocket mandrel.
- **2026-09-08 — Imports are pedantic and visible, not strict.** Parse with
  clear rules, then report the interpretation (axis chosen, surfaces accepted,
  features rejected and why) and require confirmation. Rejecting unusual input
  is unnecessary — bores, keyways and divots are filtered for free — but
  guessing silently is worse, because a wrong profile yields a plausible wind
  and a ruined part.
- **2026-09-08 — Spindle direction is configurable** (`machine.spindle_direction`),
  because which way "forward" winds depends on which side of the carriage the
  payout eye sits on, and that varies between machines. This decouples the
  handedness question from the axis-orientation question.
- **2026-09-08 — Service worker precaches with `cache: 'reload'`.** `addAll()`
  reads through the browser HTTP cache, so a stale-but-fresh-looking asset can
  be copied into the SW cache and then served indefinitely, since the SW is
  cache-first. Observed in testing; it would have shipped.
- **2026-09-08 — Repository history purged** of build artifacts (compiled
  binaries, old wasm bundles, generated G-code). `.git` went 163 MB → 784 KB.
- **2026-09-08 — Winding-angle bugs fixed** (radians-for-tangent, and a
  spurious arctangent). Laid angle now matches requested exactly.
- **2026-09-08 — Nosecones need not be wound to the tip.** A geodesic
  turnaround short of the tip is the expected result, and the turnaround radius
  `R*sin(a)` becomes a design output to report rather than a limit to defeat.
- **2026-09-08 — Axis remap is ON HOLD** pending a decision, with the 3D
  viewer now labelling every axis and the spindle direction to inform it.
- **2026-09-08 — Feedrate via G93 inverse-time.** Sidesteps the linear/rotary
  unit problem entirely by commanding duration instead of speed, and makes
  slip-driven feed variation a per-move number rather than a special mechanism.
- **2026-09-08 — `nrepeat: 0` disables a layer** rather than meaning one pass.
- **2026-09-08 — `DAOuter` is the layer's total angular span,** enabling
  absolute spindle positioning.
- **2026-09-08 — Coverage by volume conservation on a 2D (axial × angular)
  grid,** with the tow treated as a band whose projection depends on winding
  angle. Validated against closed-form hoop cases.
- **2026-09-08 — Axis letters follow lathe convention (ISO 841) by default,**
  because no filament-winding standard exists. Letters live in the machine
  profile, not the generator.
- **2026-09-08 — Flavor-agnostic internal representation**, with per-flavor
  emitters.
- **2026-09-08 — Simulation stays out of scope.**
- **2026-09-08 — Audience is a small team plus a public demonstration release.**
- **2026-09-08 — Moved off Cogent Core to a Go core + PWA.** Measured: 94% of
  the old 54 MB web bundle was GUI framework; the winding logic was 3 MB. Every
  bug found in a full session came from the framework, none from the winding
  math. Now a pure `internal/wind` core compiled to wasm (1.27 MB gzipped),
  UI in HTML/CSS/JS with three.js. Module deps 40 → 1.
- **2026-09-08 — PWA before native desktop.** The two share nearly all their
  work, so the PWA is a prefix of the native path, not a detour.
- **2026-09-08 — File System Access API for saving,** with a download fallback
  marked temporary. Chromium-only.
- **2026-09-08 — IndexedDB draft autosave is a cache, not the system of
  record.** It is evictable.
- **2026-09-08 — Blocks and JSON edit one shared model.**
- **2026-09-08 — Service worker generated at build time** with a content hash,
  so an unchanged build produces no update and no re-downloads.

---

## 9. Open questions

1. ~~How is output verified?~~ **ANSWERED: by eye, in the 3D view.** No
   hardware access at present. That raises the value of the coverage plot and
   the slip check considerably — they are currently the only things that can
   catch a bad wind before it reaches a machine. It also means the test suite
   (L6) should target the *core*, where correctness is checkable against
   closed-form results, rather than against machine behaviour.
2. ~~Is `gcode_gen` still the reference?~~ **ANSWERED: no.** Cocoon is now
   authoritative and free to diverge. The compatibility claims in the code have
   been removed; the original algorithm survives only as commented derivation
   where it aids understanding.
3. ~~Largest realistic wind?~~ **ANSWERED and MEASURED.** See §9.1.
### 9.1 Worst-case sizing — measured

Stated absolute worst case: an **8" x 4' tube**, or an **8" base x 3' nosecone**,
at a **0.2" (5.08 mm) wall**, wound with thin E-glass roving. Taking the tow as
**3 mm wide x 0.1 mm thick** (the thin end of practical single-end roving; a
thinner tow means more circuits, so this is the pessimistic choice), the wall
needs ~51 layers.

| Case | Points per layer | Total (51 layers) | Point memory |
|---|---|---|---|
| Tube, all hoop | 29,638 | 1.5 M | 0.06 GB |
| Tube, helical 45° | 295,960 | 15.1 M | 0.6 GB |
| **Tube, helical 15°** | **807,520** | **41.2 M** | **1.6 GB** |
| Nosecone, helical 15° | 606,464 | 30.9 M | 1.2 GB |

**The finding: the per-layer guard was correctly sized, the aggregate one was
missing.** The heaviest single layer is ~808k points, comfortably inside the
5M per-layer limit — but the total reaches ~41 million points, about 1.6 GB of
`Point` structs before the renderer's vertex buffer is even allocated.

`MaxTotalPoints` (8 M) now bounds the whole wind, chosen so that Points plus
vertices stay inside what a browser tab can hold. The absolute worst case above
therefore **does not fit**, and fails with a message rather than an OOM.

Supporting it would need a change of strategy, not a bigger number:

- **Stream to G-code** instead of materialising every layer's full path, keeping
  only aggregate metrics.
- **Decimate the render geometry** — the viewer does not need every point; an
  LOD pass would cut vertices by an order of magnitude with no visible loss.

Both are worth doing eventually; neither is needed for realistic parts, which
sit one to two orders of magnitude below the cap.

4. **Does the bucket (L2) exist to solve CSV-profiles-on-web, or is it a goal
   in its own right?** If the former, resolving profile references through an
   abstraction is the cheaper fix.
5. **Ordering.** Items 1–4 are **DONE**:
   1. ~~The angle formula fix~~ (§4.1b) — done, exact at every angle.
   2. ~~Axis remap + feedrate~~ — lathe letters with aliasing, G93 inverse-time
      with a G94 fallback, surface speed as the physical input.
   3. ~~Filament usage and time estimates~~ — `ComputeMetrics`, cross-checked
      against the coverage grid.
   4. ~~Header/footer with embedded config~~ — provenance header, layer and
      coverage detail in the footer, config recoverable byte-identically.

   Remaining:
   5. **Mandrel geometry / CAD import** (§10) — IN PROGRESS. Biggest usability
      lever, and slip work wants real mandrel shapes to be worth much.
   6. **Slip checking** (§4.1) — scheduled next, after import. See §11.
   7. ~~Mandrel growth feedback~~ (CV5) — **deprioritised.** At the wall
      thicknesses in play the radius change per layer is a fraction of a
      millimetre against a 50 mm radius, so feeding it back would move the
      geometry by well under a percent. Revisit only if thick-wall parts appear.

---

## 10. Mandrel geometry sources

**This is the biggest usability lever in the project.** Most mandrel geometry
already exists in CAD before Cocoon ever sees it, so the question is not "what
file format do we invent" but "how many of the tools people already use can we
accept work from".

### Should profiles stay in separate CSVs?

The original reasons were cleanliness when hand-editing JSON, and sharing one
geometry across several wind files. The first no longer applies — the block
editor means nobody has to read raw JSON — and the second is better served by
making geometry cheap to express than by making it a separate file.

**Recommendation: support both, and make inline the default.**

- **Inline** (`profile: [[x, r], ...]`, already supported) makes a wind file
  self-contained. It also removes the web CSV problem entirely rather than
  working around it, because there is no file to resolve.
- **By reference** stays available for genuinely shared geometry, resolved
  through an abstraction rather than `os.Open`, so desktop and web can back it
  differently.

Note the header now embeds the profile alongside the config, so a *generated
program* is self-contained regardless of which the source used.

### Parametric curves — highest value, lowest effort

Rather than storing sampled points at all, define the profile mathematically
and sample it at whatever resolution the path generator needs.

For rocketry the useful set is small:

| Shape | Parameters |
|---|---|
| Cylinder | length, diameter |
| **Tangent ogive** | base radius, fineness ratio (or length), **tip radius** |
| Conical | base radius, length, tip radius |
| Elliptical | base radius, length |
| Von Kármán / LV-Haack | base radius, length |
| Power series | base radius, length, exponent |
| Composite | a sequence of the above, joined |

The **tip radius** matters: a winder cannot wind a sharp point, so a blunted
tip is not an approximation but a requirement — and it is exactly the parameter
that a sampled CSV makes awkward to adjust.

Advantages over sampled points: exact rather than interpolated, resolution-
independent, a handful of numbers instead of dozens of rows, and the parameters
are the ones a designer actually thinks in. Iterating a fineness ratio becomes
editing one field instead of regenerating a CSV.

### Importing from CAD

Target packages: **Onshape and Fusion 360 primarily, some SolidWorks.**

Ranked by value per unit of effort:

1. **Paste a point table — do this first.** Every one of those packages can
   produce a list of coordinates, and a textarea that accepts pasted
   `x, r` rows needs no parser, no format detection, and no axis guessing. It
   is an afternoon of work and it unblocks every workflow immediately, however
   awkwardly. Ship it as the floor, then make the nicer paths better than it.

2. **DXF — useful second, no longer the main route.** A 2D
   cross-section sidesteps the "is this really axisymmetric" question entirely,
   because the profile *is* the input rather than something inferred from it.
   All three packages export DXF from a sketch or drawing.

   One caveat worth knowing before starting: **curved profiles usually export
   as SPLINE entities**, which are NURBS and need evaluating — DXF is only
   trivially parseable for LINE/ARC/LWPOLYLINE. Budget for a small NURBS
   evaluator. There is also a semantic problem shared with every other format:
   which entities are the profile, and where is the axis? Construction lines,
   dimensions and the centreline all arrive in the same file.

3. **STEP — CHOSEN as the primary import route (2026-09-08).**
   Full STEP parsing is a large job, which is why it looks unattractive. But a
   revolved mandrel does not need full parsing: the geometry is carried in a
   small family of analytic surface entities that state their axis and radius
   explicitly, so the profile is read rather than inferred. That removes the
   axis ambiguity affecting DXF and STL both, and degrades correctly — a model
   that is not axisymmetric simply produces no coaxial surfaces, which is the
   right answer instead of a wrong profile.

   See §10.1–10.4 for the entity model, edge-case analysis, and the export
   instructions that keep it unambiguous.

4. **STL — the robust fallback.** Universally exported and trivial to parse.
   For an axisymmetric mandrel: per vertex compute `r = sqrt(y² + z²)`, bin by
   `x`, take the max radius. Your concern is the right one — the part may not
   actually be symmetric — but that is *detectable*: the spread of radii within
   each bin measures it directly, so an asymmetric model can be rejected with a
   number rather than producing a silently wrong profile.

5. **Onshape REST API — worth considering for the primary tool.** Onshape is
   cloud-native with a documented public API, so a profile could be pulled from
   a document link rather than exported at all. Highest fidelity and lowest
   user friction of any option, at the cost of being per-CAD work and needing
   auth. Fusion has an add-in API that could do the same locally.

6. **SVG — deprioritised.** Superseded by DXF for this purpose.

### 10.1 STEP import — entity model

**A cylinder is not a `SURFACE_OF_REVOLUTION`.** ISO 10303-42 defines a family
of simple surfaces, and revolved geometry lands in whichever is most specific:

| Entity | Gives radius how | Where it shows up on a mandrel |
|---|---|---|
| `CYLINDRICAL_SURFACE` | radius attribute | the body |
| `CONICAL_SURFACE` | radius + semi-angle | conical sections |
| `SPHERICAL_SURFACE` | radius | rounded tip or end cap |
| `TOROIDAL_SURFACE` | two radii | fillet between sections |
| `SURFACE_OF_REVOLUTION` | evaluate the generating curve | ogives, general profiles |
| `PLANE` | — | flat ends (bounds only) |

So the parser must handle the whole family, not just `SURFACE_OF_REVOLUTION` —
a parser looking only for the latter would miss the cylindrical body of every
rocket mandrel. That is mostly good news: four of the five give `r(x)` in
closed form straight from attributes, and only `SURFACE_OF_REVOLUTION` needs
curve evaluation (and only when the generating curve is a B-spline rather than
a line or arc).

### 10.2 Edge cases

Two filters do nearly all the work:

1. **Coaxial** — keep only surfaces whose axis is collinear with the mandrel
   axis.
2. **Outermost** — at each axial station, take the largest radius.

Most of what makes a real mandrel model "messy" is excluded by one or the other
without special-casing, because bores, keyways and divots are each either
not axisymmetric, not coaxial, or not outermost.

| Case | Difficulty | How it resolves |
|---|---|---|
| **Cylinder + ogive stacked** | Moderate | Not really an edge case — it is the normal shape. Both surfaces are coaxial; order them by axial extent and concatenate. The work is getting extents (see below). |
| **Coaxial bore / keyway hole** | Easy | The bore is coaxial but *smaller*, so the outermost rule drops it. The keyway slot itself is planar, not axisymmetric, so it never enters. Free. |
| **Locating divots / nubs** | Easy | A divot's cylinder or sphere axis is radial, not axial, so the coaxial filter rejects it. Free — though a protruding nub is then silently ignored, which is correct for the winding surface but worth reporting. |
| **Split lengthwise (half shells)** | Easy geometry, moderate detection | The *surface* entity is still the full revolve; only the face bound is partial. So the profile reads correctly even from one half. Detecting that it was a half is the harder part, and worth a warning rather than a rejection. |
| **Split into axial segments** | Easy within one part | Same as the stacked case. Becomes harder only if exported as an **assembly**, where each part carries its own placement transform that must be composed. Avoidable by instruction. |

**The genuinely hard parts** are not in that list:

- **Face extents.** STEP surfaces are unbounded; the bounds live in the
  topology (`ADVANCED_FACE` → `FACE_OUTER_BOUND` → `EDGE_LOOP` → `EDGE_CURVE` →
  `VERTEX_POINT`). Knowing where the cylinder stops and the ogive starts means
  walking that. Tractable — collect the vertex points per face and project onto
  the axis — but it is the bulk of the work.
- **Assembly transforms.** Multi-part files place each solid with its own
  transformation. Composing those is real work and entirely avoidable by asking
  for a single-body export.
- **NURBS evaluation** for B-spline generating curves.
- **Units.** STEP carries them explicitly; they must be read, not assumed.

### 10.3 Strict input, or tolerant parsing?

**Neither — be pedantic and *visible*.** Rejecting anything unusual is
frustrating and, given the analysis above, unnecessary: the cases that look
alarming are mostly free. But silently guessing is worse, because a wrong
profile produces a plausible-looking wind and a ruined part.

The right shape is: parse with clear rules, then **report the interpretation
and require confirmation**:

- the axis chosen, and why
- each coaxial surface found, its type, radius range and axial extent
- everything rejected, with the reason (not coaxial / not outermost / not
  axisymmetric)
- the resulting profile drawn in the 3D view before it is accepted (**MG9**)

That turns every edge case above into something the user can see and judge,
rather than something the parser has to be right about unaided. It also makes
the export instructions advice rather than law — following them makes the
report trivial to read, not the difference between working and failing.

### 10.4 Export instructions (to be shipped in-app)

Written to make parsing pedantic and unambiguous:

1. **Export STEP AP214 or AP242.** AP203 works but carries less metadata.
2. **Export a single body, not an assembly.** Assemblies add placement
   transforms that must be composed; one body avoids the whole class of
   problem. If the mandrel is split for printing, export the *assembled*
   solid, or one segment at a time.
3. **Align the mandrel axis to a global axis** (X or Z) and put the origin at
   one end. Off-axis geometry is detectable but makes the report harder to
   read, and an eccentric model becomes ambiguous.
4. **Model the winding surface as a revolve** where possible. Lofts and
   extrudes can produce B-spline surfaces that are not surfaces of revolution
   at all, in which case there is no exact profile to recover.
5. **Blunt the tip.** A winder cannot wind a point; a tip radius is required
   geometry, not an approximation.
6. **Bores, keyways, divots and nubs can stay.** They are filtered out
   automatically. They will appear in the import report as rejected features,
   which is a useful cross-check that the right surface was found.
7. **Units: millimetres preferred.** They are read from the file either way,
   but mm avoids a conversion to double-check.

**Requirements:**

- **MG11** Handle the full simple-surface family, not just
  `SURFACE_OF_REVOLUTION`.
- **MG12** Select the mandrel axis by coaxial-surface consensus, and report it.
- **MG13** Report every accepted and rejected surface with its reason, and
  require confirmation before use.
- **MG14** Warn on partial revolves (half shells) and on assembly files.
- **MG15** Read units from the file rather than assuming millimetres.

### 10.5 STEP library evaluation — decided: write the Part 21 reader

Surveyed 2026-09-10.

| Option | Analytic surfaces? | Size | Verdict |
|---|---|---|---|
| **occt-import-js** | **No — tessellates** | ~3 MB wasm | Returns triangle meshes only, which throws away the radius/axis data that made STEP worth choosing over STL |
| opencascade.js / libcascade | Yes (full OCCT API) | tens of MB | Dwarfs the 1.3 MB core for an occasional feature |
| occt-wasm | Mesh-oriented | ~4 MB brotli | Same problem as occt-import-js |
| STEPcode | Yes | C/C++ | **Go's wasm target has no cgo**, so it cannot be linked into the core at all |
| Pure-Go STEP libraries | — | — | Nothing mature found |
| **Write a Part 21 reader** | **Yes** | ~0 | **Chosen** |

Two findings decided this. First, the browser-ready importers **tessellate** —
their whole purpose is feeding a mesh viewer, so they hand back triangles and
the analytic surfaces are gone. That leaves us doing axisymmetric extraction
from a mesh, which is the STL approach with extra steps. Second, **Go's wasm
target does not support cgo**, so every complete C/C++ implementation is
unreachable from the core regardless of size.

Writing it is smaller than it sounds. Part 21 is a simple textual format —
`#12 = CARTESIAN_POINT('', (0., 1., 2.));` — with a value grammar of eight
kinds and one wrinkle (complex instances). A mandrel needs roughly fifteen of
the thousands of entity types in STEP, so the parser does not need to *know*
the schema at all: it builds an addressable entity table and leaves
interpretation to the caller.

**Status: implemented and tested** (`internal/wind/step`, 464 lines). Verified
against a file exercising the §10.2 edge cases: it reads the header and schema,
finds `CYLINDRICAL_SURFACE` / `CONICAL_SURFACE` / `SURFACE_OF_REVOLUTION` with
correct radii and placements, chases references, keeps complex instances
intact, and skips comments. Six tests cover the value grammar (including
doubled-quote escapes and scientific notation), the edge-case discrimination,
and rejection of non-STEP input. **These are the project's first automated
tests** (L6).

Remaining work for the importer proper: the semantic layer — coaxial grouping,
face extents from topology, NURBS evaluation for B-spline generating curves,
and unit conversion.

### 10.6 Should the source file be embedded in the design JSON?

**No for the raw file; yes for the extracted profile and its provenance.**

Against embedding the STEP itself:

- Mandrel STEP files run from ~100 KB to several MB, and base64 adds a third
  on top.
- The design JSON is **autosaved to IndexedDB on every keystroke** and
  round-tripped through the block editor. A multi-megabyte blob in that model
  makes every edit and every save carry it.
- The **G-code header embeds the config verbatim**, so an embedded source file
  would land in every generated program too — the header is a few KB today.
- It is not readable or editable by a human, so it fails the test the rest of
  the format passes.

Embed instead:

- **The extracted profile points**, which is the inline-profile plan anyway:
  small, readable, editable, and self-contained.
- **Provenance metadata**: source filename, content hash, import date, the
  chosen axis, and which surfaces were used. Enough to know where geometry came
  from and to detect that the source has changed since.
- **The parametric fit**, when fitting succeeds — smaller still, and better.

That reproduces the *result* without carrying the *input*. If the original is
wanted, the CAD system is where it lives.

**Middle ground worth offering later:** keep the raw file in the browser
bucket (L2) keyed by its hash, referenced from the design rather than inlined.
Retained, deduplicated, and out of the hot path.

### 10.7 Import interface

- **Entry point:** an "Import from CAD…" button in the Mandrel block, opening
  the file picker filtered to `.step` / `.stp`.
- **Import review panel**, shown before anything is committed:
  - the axis chosen, and the evidence for it
  - every accepted surface: type, radius range, axial extent
  - every rejected feature, with the reason (not coaxial / not outermost / not
    axisymmetric)
  - the resulting profile drawn in the viewport
- **Selection in the viewport** — worth building. Render the candidate surfaces
  as separately pickable objects so the user can click to include or exclude
  one, and pick the axis when the guess is ambiguous. Three.js `Raycaster`
  makes the picking straightforward; the work is keeping surface identity from
  the parse through to the rendered object.

  This is the "pedantic and visible" principle (§10.3) made concrete: instead
  of the parser having to be right unaided, the user confirms an interpretation
  they can see.
- **Commit** converts to inline points (or a fitted parametric shape) once, at
  import. Generation runs on every keystroke and must never re-parse CAD.

### Fit imported geometry to a parametric curve

Whatever the source, offer to **fit the imported points to one of the
parametric shapes** and switch to that representation. Import once, then edit a
fineness ratio instead of regenerating a file. It also turns a noisy scan or a
coarse tessellation into an exact surface, and the residual of the fit is a
useful quality signal about the import.

**Requirements:**

- **MG1** Support inline profiles as the self-contained default.
- **MG2** Resolve referenced profiles through an abstraction, not the OS
  filesystem, so desktop and web can back it differently.
- **MG3** Add a parametric mandrel type with the rocketry shapes above,
  including a blunted tip radius.
- **MG4** Accept a **pasted point table** as the universal floor.
- **MG5** Import from **STEP** — the primary route; see §10.1–10.4.
- **MG6** Import from **DXF** (2D cross-section), including SPLINE evaluation.
- **MG7** Import from **STL** by axisymmetric extraction, with a measured
  symmetry check rather than an assumption.
- **MG8** Offer to **fit an imported profile to a parametric shape**.
- **MG9** Show the imported/derived profile in the 3D view before committing to
  it, so a bad axis guess or unit error is visible immediately.
- **MG10** Convert an imported profile to points **once, on import**, rather
  than re-parsing CAD files during generation. Import is a user action;
  generation runs on every keystroke.

### Deployment

The app is a static site and deploys to **GitHub Pages** as-is. Verified: all
asset references are relative, so a project-page subpath (`/cocoon/`) resolves
correctly, and the service worker's precache list uses `./` paths that resolve
against its own scope.

The one blocker is that the build outputs (`cocoon.wasm`, `wasm_exec.js`,
`sw.js`) are gitignored, so a repo-sourced Pages deploy would publish an
incomplete site. `.github/workflows/pages.yml` builds them in CI instead, which
keeps the repository source-only — committing a ~5 MB wasm per build is what
grew `.git` to 163 MB before its history was purged.

---

## 11. Slip checking — plan

Scheduled after CAD import (§10), because the check is most valuable on the
tapered mandrels that import will make easy to work with, and near-trivial on
the cylinders that are easy to define by hand.

Recall the model (§4.1): a **geodesic** path needs no friction and satisfies
`r·sin(α) = const`; any deviation is **non-geodesic**, held by friction, and
characterised by a **slippage coefficient λ** that must stay under the static
friction coefficient μ. Setting λ = 0 recovers the geodesic case, so it is one
framework rather than two.

### Phase 1 — measure

- **S1.1** Compute the local winding angle `α(x)` along each generated path.
  Available already: `tan(α) = r·dθ/dx` per segment.
- **S1.2** Compute the Clairaut constant implied by each layer's start, so the
  geodesic reference `α_geo(x) = arcsin(r₀/r)` can be compared against what the
  layer actually does.
- **S1.3** Compute **λ along the path**. This is the part that needs deriving
  properly from the literature rather than guessed — λ is the ratio of
  geodesic to normal curvature for the path on the surface, and getting the
  surface-of-revolution expression wrong would produce confident, wrong
  numbers. Budget reading time, and validate against the two cases with known
  answers: λ = 0 for a true geodesic, and λ = 0 everywhere on a cylinder at
  constant angle.

### Phase 2 — report

- **S2.1** A **λ plot against axial position**, alongside the coverage plot,
  with μ drawn as a threshold line. Same shape as the coverage work, so it
  reuses that plotting code.
- **S2.2** **Highlight at-risk spans in the 3D view** — colour the path where
  λ > μ. With verification being by eye, seeing *where* it slips matters more
  than knowing that it does.
- **S2.3** Report each helical layer's **geodesic turnaround radius and axial
  position** (**SL6**), which is useful on its own and falls out of S1.2.

### Phase 3 — act

- **S3.1** Make **μ configurable**, per filament/process (literature values run
  roughly 0.2–0.39, condition-dependent).
- **S3.2** Replace the `180 − 2·angle` turnaround heuristic with a λ-based one
  (**SL4**).
- **S3.3** Add **geodesic mode** (**SL5**), deriving `α(x)` from Clairaut so a
  wind is friction-independent by construction.
- **S3.4** Optionally **vary feedrate** where λ approaches μ — the machinery
  for this already exists, since G93 makes per-move duration a free parameter
  (**FR5**).

### Open question

Should exceeding μ be a **hard error** or a **warning**? Leaning warning: μ is
an estimate with real spread, the operator may know their setup better than the
number does, and a hard block on an uncertain threshold will get worked around
rather than heeded.

---

## Sources

Axis conventions and slip model surveyed 2026-09-08:

- [Filament Winding Machines — Cadfil](https://www.cadfil.com/filamentwinding.html)
- [X-Winder 4-Axis Filament Winder](https://3dprint.com/53440/x-winder-filament-winder/)
- [Filament winding — Wikipedia](https://en.wikipedia.org/wiki/Filament_winding)
- [Slippage coefficient measurement for non-geodesic filament-winding process](https://www.sciencedirect.com/science/article/abs/pii/S1359835X10003106)
- [Influence of slippage coefficient on the non-geodesic return trajectory at mandrel extremities](https://www.researchgate.net/publication/326526071_Influence_of_slippage_coefficient_on_the_non-geodesic_return_trajectory_at_mandrels_extremities_in_filament_winding_process)
- [Non-geodesic filament winding on generic shells of revolution](https://www.researchgate.net/publication/245390030_Non-geodesic_filament_winding_on_generic_shells_of_revolution)
