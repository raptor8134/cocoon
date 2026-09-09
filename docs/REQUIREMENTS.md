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

> +C about +Z rotates X toward Y (right-hand rule). Forward sweeps the mandrel
> surface up and over the back, so **X = up** and **Y = back**, which makes
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
3. **Only the first move is a rapid**; everything after is a feed move.
4. **Progress markers**: `min(100, points/10)`, evenly spaced by index.
   Superseded once FR1/FR2 land — they should key off elapsed time.
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
7. **Filament width is the band width laid down**, used for coverage;
   thickness would be radial buildup.

---

## 8. Decisions made

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

1. **How do you verify output today?** Eyeball the 3D view, dry-run the
   machine, or wind a real part? Decides whether golden-file tests against the
   Python original are worth building.
2. **Is Python `gcode_gen` still the reference?** If output must match it
   exactly, that's a testable contract. If Cocoon is now authoritative, several
   "match Python" comments in the code should go.
3. **What's the largest realistic wind?** Current safety limit is 5M points per
   layer.
4. **Does the bucket (L2) exist to solve CSV-profiles-on-web, or is it a goal
   in its own right?** If the former, resolving profile references through an
   abstraction is the cheaper fix.
5. **Ordering.** Thickness and coverage are now done. Remaining large pieces,
   in suggested order:
   1. **The angle formula fix** (§4.1b) — smallest change, largest correctness
      impact, and everything downstream inherits the error until it lands.
   2. **Flavor abstraction + axis remap + G93 feedrate** — these are one piece
      of work; splitting them means doing the emitter twice.
   3. **Filament usage and time estimates** — falls out of FR1/FR2 once feed is
      real, and unblocks honest progress markers and the header block.
   4. **Header/footer with embedded config** (§4.4).
   5. **Slip checking** (§4.1) — most research-heavy, and benefits from having
      coverage and real feedrates in place.
   6. **Mandrel growth feedback** (CV5) — small, but changes multi-layer output,
      so it wants a deliberate moment.

---

## Sources

Axis conventions and slip model surveyed 2026-09-08:

- [Filament Winding Machines — Cadfil](https://www.cadfil.com/filamentwinding.html)
- [X-Winder 4-Axis Filament Winder](https://3dprint.com/53440/x-winder-filament-winder/)
- [Filament winding — Wikipedia](https://en.wikipedia.org/wiki/Filament_winding)
- [Slippage coefficient measurement for non-geodesic filament-winding process](https://www.sciencedirect.com/science/article/abs/pii/S1359835X10003106)
- [Influence of slippage coefficient on the non-geodesic return trajectory at mandrel extremities](https://www.researchgate.net/publication/326526071_Influence_of_slippage_coefficient_on_the_non-geodesic_return_trajectory_at_mandrels_extremities_in_filament_winding_process)
- [Non-geodesic filament winding on generic shells of revolution](https://www.researchgate.net/publication/245390030_Non-geodesic_filament_winding_on_generic_shells_of_revolution)
