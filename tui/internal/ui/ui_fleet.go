package ui

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/host"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/session"
	"github.com/skymule-ai/mullet_sitl_sim/tui/internal/theme"
)

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ff(f float64) string { return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64) }

func isFloat(s string) error {
	if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
		return errors.New("a number")
	}
	return nil
}

func atof(s string) float64 { f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return f }

func noHash(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	if strings.Contains(s, "#") {
		return errors.New("'#' is not allowed")
	}
	return nil
}

func matches(re string) func(string) error {
	r := regexp.MustCompile("^" + re + "$")
	return func(s string) error {
		if !r.MatchString(s) {
			return fmt.Errorf("must match %s", re)
		}
		return nil
	}
}

// problemLines renders spawn_fleet.py problems: errors in red, warnings in amber.
func problemLines(problems []string) []string {
	var out []string
	for _, p := range problems {
		if strings.Contains(p, "warning") {
			out = append(out, "  "+theme.Current.Warn.Render("⚠ "+strings.TrimPrefix(p, "[mullet] warning: ")))
		} else {
			out = append(out, "  "+theme.Current.Stop.Render(theme.Current.I.Failed+" "+strings.TrimPrefix(p, "Config error: ")))
		}
	}
	return out
}

// --- fleet screen ---

func (a *app) viewFleet() (string, string) {
	s := a.sess
	lines := []string{"", fmt.Sprintf("  %s  %s", theme.Current.Primary.Render("FLEET"), theme.Current.Bold.Render(s.Name)) +
		theme.Current.Muted.Render(fmt.Sprintf("   %d robots · world %s · namespace /%s_<N>", len(s.Fleet), s.World, orDefault(s.NSPrefix, "px4"))), ""}
	lines = append(lines, theme.Current.Muted.Render(fmt.Sprintf("     %-4s %-14s %-12s %-22s %-10s %-6s %s", "INST", "TYPE", "ID", "MODEL", "NAMESPACE", "SYSID", "POSE x, y, z")))
	a.sel = max(0, min(a.sel, len(s.Fleet)-1))
	for i, r := range s.Fleet {
		marker := "  "
		id := theme.Current.Text.Render(fmt.Sprintf("%-12s", r.ID))
		if i == a.sel {
			marker = theme.Current.Accent.Render("▸ ")
			id = theme.Current.Bold.Render(fmt.Sprintf("%-12s", r.ID))
		}
		row := fmt.Sprintf(" %s%s %-4d %s %-12s %s %s %s %s",
			marker, theme.Current.Robot(r.Instance).Render(theme.Current.I.Ready), r.Instance,
			theme.Current.Robot(r.Instance).Render(theme.Current.I.Robot(r.Model)), theme.RobotType(r.Model), id,
			theme.Current.Text.Render(fmt.Sprintf("%-22s", r.Model)), theme.Current.Accent.Render(fmt.Sprintf("%-10s", s.Namespace(r.Instance))),
			theme.Current.Muted.Render(fmt.Sprintf("%-6d %s, %s, %s", r.Instance+1, ff(r.Pose[0]), ff(r.Pose[1]), ff(r.Pose[2]))))
		a.hits = append(a.hits, hit{Rect{X: 0, Y: 1 + len(lines), W: min(a.w, 100), H: 1}, fmt.Sprintf("row:%d", i)})
		lines = append(lines, row)
	}
	if len(s.Fleet) == 0 {
		lines = append(lines, "  "+theme.Current.Muted.Render("No robots yet — press a to add one."))
	}
	lines = append(lines, "")
	if len(s.Commands) > 0 {
		lines = append(lines, "  "+theme.Current.Muted.Render(fmt.Sprintf("code window: %d command(s) · c to edit", len(s.Commands))))
	}
	lines = append(lines, problemLines(a.problems)...)
	body := strings.Join(lines, "\n")

	if a.w >= 130 && len(s.Fleet) > 0 {
		var pts []mapPoint
		for _, r := range s.Fleet {
			pts = append(pts, mapPoint{r.Pose[0], r.Pose[1], strconv.Itoa(r.Instance), theme.Current.P.RobotColor(r.Instance)})
		}
		mw, mh := 34, 14
		body = joinColumns(body, a.w-mw-2, theme.Current.Muted.Render(" spawn map (x → , y ↑)")+"\n"+fleetMap(pts, mw, mh))
	}
	return body, "a add · d duplicate · x delete · enter edit · f formation · c commands · o options · s save · L launch · esc home"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (a *app) updateFleet(msg tea.Msg) tea.Cmd {
	s := a.sess
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		var i int
		if _, err := fmt.Sscanf(a.hitAt(m.X, m.Y), "row:%d", &i); err == nil {
			a.sel = i
		}
	case tea.KeyPressMsg:
		switch m.String() {
		case "up", "k":
			a.sel = max(0, a.sel-1)
		case "down", "j":
			a.sel = min(len(s.Fleet)-1, a.sel+1)
		case "a":
			r := Robot{Model: "gz_x500", Pose: [6]float64{0, 0, 0.2}}
			if n := len(s.Fleet); n > 0 {
				last := s.Fleet[n-1]
				r.Model, r.Pose = last.Model, last.Pose
				r.Pose[0] += 3
			}
			r.Instance = s.NextInstance()
			r.ID = fmt.Sprintf("%s_%d", orDefault(s.NSPrefix, "px4"), r.Instance)
			return a.editRobot(r, -1)
		case "d":
			if len(s.Fleet) > 0 {
				r := s.Fleet[a.sel]
				r.Instance = s.NextInstance()
				r.ID = fmt.Sprintf("%s_%d", orDefault(s.NSPrefix, "px4"), r.Instance)
				r.Pose[0] += 3
				r.Params = append([]string(nil), r.Params...)
				s.Fleet = append(s.Fleet, r)
				a.sel = len(s.Fleet) - 1
				a.validate()
			}
		case "x", "delete", "backspace":
			if len(s.Fleet) > 0 {
				s.Fleet = append(s.Fleet[:a.sel], s.Fleet[a.sel+1:]...)
				a.validate()
			}
		case "enter", "e":
			if len(s.Fleet) > 0 {
				return a.editRobot(s.Fleet[a.sel], a.sel)
			}
		case "f":
			return a.formation()
		case "c":
			a.cmdSel, a.screen = 0, scrCommands
		case "o":
			return a.editOptions()
		case "s":
			a.save()
		case "L", "ctrl+l":
			return a.startLaunch()
		case "esc", "q":
			a.screen = scrHome
		}
	}
	return nil
}

func (a *app) save() bool {
	a.validate()
	if err := a.sess.Save(a.repo); err != nil {
		a.notify(err.Error(), true)
		return false
	}
	a.set.LastSession = a.sess.Path()
	_ = a.set.Save()
	msg := "Saved " + a.sess.Path()
	if session.HasErrors(a.problems) {
		a.notify(msg+" (has errors: fix them before launching)", true)
	} else {
		a.notify(msg, false)
	}
	return true
}

// editRobot opens the robot form; idx -1 adds a new robot.
func (a *app) editRobot(r Robot, idx int) tea.Cmd {
	s := a.sess
	id, inst, model := r.ID, strconv.Itoa(r.Instance), r.Model
	x, y, z, yaw := ff(r.Pose[0]), ff(r.Pose[1]), ff(r.Pose[2]), ff(r.Pose[5])
	params := strings.Join(r.Params, ", ")
	used := map[int]bool{}
	for i, o := range s.Fleet {
		if i != idx {
			used[o.Instance] = true
		}
	}

	var modelField huh.Field
	if img, ok := a.image(); ok && len(img.Airframes) > 0 {
		afs := append([]Airframe(nil), img.Airframes...)
		sort.SliceStable(afs, func(i, j int) bool { return theme.RobotType(afs[i].Model) < theme.RobotType(afs[j].Model) })
		var opts []huh.Option[string]
		for _, af := range afs {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s %-11s %-24s %d", theme.Current.I.Robot(af.Model), theme.RobotType(af.Model), af.Model, af.Autostart), af.Model))
		}
		modelField = huh.NewSelect[string]().Title("Model").Description("from " + img.Ref + " · / to filter").Options(opts...).Value(&model).Height(10)
	} else {
		modelField = huh.NewInput().Title("Model").Description("no probed image: type a gz model name").Placeholder("gz_x500").Value(&model).Validate(noHash)
	}
	title := theme.Current.I.Add + " New robot"
	if idx >= 0 {
		title = "Edit " + r.ID
	}
	f := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("ID").Value(&id).Validate(noHash),
			huh.NewInput().Title("Instance").Description("namespace /<prefix>_<instance> · MAV_SYS_ID = instance+1 · keep ≤ 9 for per-robot MAVSDK").
				Value(&inst).Validate(func(v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				switch {
				case err != nil || n < 0 || n > 254:
					return errors.New("0–254")
				case used[n]:
					return errors.New("instance already used")
				}
				return nil
			}),
			modelField,
		).Title(title),
		huh.NewGroup(
			huh.NewInput().Title("x (m)").Value(&x).Validate(isFloat),
			huh.NewInput().Title("y (m)").Value(&y).Validate(isFloat),
			huh.NewInput().Title("z (m)").Value(&z).Validate(isFloat),
			huh.NewInput().Title("yaw (rad)").Value(&yaw).Validate(isFloat),
			huh.NewInput().Title("PX4 params").Description("NAME=VALUE, comma separated · MAV_SYS_ID / UXRCE_DDS_* are derived").Value(&params),
		).Title("Spawn pose & PX4 params"),
	)
	return a.openForm(f, scrFleet, func() tea.Cmd {
		if model != r.Model {
			r.Autostart = 0 // derived from the model in the container
		}
		r.ID, r.Model = strings.TrimSpace(id), strings.TrimSpace(model)
		r.Instance, _ = strconv.Atoi(strings.TrimSpace(inst))
		r.Pose[0], r.Pose[1], r.Pose[2], r.Pose[5] = atof(x), atof(y), atof(z), atof(yaw)
		r.Params = splitList(params)
		if idx < 0 {
			s.Fleet = append(s.Fleet, r)
			a.sel = len(s.Fleet) - 1
		} else {
			s.Fleet[idx] = r
		}
		a.validate()
		return nil
	})
}

func (a *app) formation() tea.Cmd {
	s := a.sess
	if len(s.Fleet) == 0 {
		return nil
	}
	pattern, spacing, z := "line", "3", ff(s.Fleet[0].Pose[2])
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Pattern").Options(
			huh.NewOption("Line along x", "line"), huh.NewOption("Grid", "grid"), huh.NewOption("Circle", "circle")).Value(&pattern),
		huh.NewInput().Title("Spacing (m)").Value(&spacing).Validate(isFloat),
		huh.NewInput().Title("z (m)").Value(&z).Validate(isFloat),
	).Title("Formation").Description("Re-places every robot's spawn pose"))
	return a.openForm(f, scrFleet, func() tea.Cmd {
		sp, n := atof(spacing), len(s.Fleet)
		cols := int(math.Ceil(math.Sqrt(float64(n))))
		radius := math.Max(sp, sp*float64(n)/(2*math.Pi))
		for i := range s.Fleet {
			var x, y float64
			switch pattern {
			case "grid":
				x, y = float64(i%cols)*sp, float64(i/cols)*sp
			case "circle":
				ang := 2 * math.Pi * float64(i) / float64(n)
				x, y = radius*math.Cos(ang), radius*math.Sin(ang)
			default:
				x = float64(i) * sp
			}
			s.Fleet[i].Pose[0], s.Fleet[i].Pose[1], s.Fleet[i].Pose[2] = math.Round(x*100)/100, math.Round(y*100)/100, atof(z)
		}
		a.validate()
		return nil
	})
}

// --- code commands ---

func (a *app) viewCommands() (string, string) {
	s := a.sess
	lines := []string{"", "  " + theme.Current.Primary.Render("CODE WINDOW") + theme.Current.Muted.Render("   one pane per command, run inside the container"), ""}
	a.cmdSel = max(0, min(a.cmdSel, len(s.Commands)-1))
	for i, c := range s.Commands {
		auto := theme.Current.Muted.Render(theme.Current.I.Stopped + " typed")
		if c.Autorun {
			auto = theme.Current.OK.Render(theme.Current.I.Ready + " autorun")
		}
		marker := "  "
		if i == a.cmdSel {
			marker = theme.Current.Accent.Render("▸ ")
		}
		a.hits = append(a.hits, hit{Rect{X: 0, Y: 1 + len(lines), W: a.w, H: 1}, fmt.Sprintf("cmd:%d", i)})
		lines = append(lines, fmt.Sprintf("  %s%-12s %s", marker, auto, theme.Current.Text.Render(c.Run)))
	}
	if len(s.Commands) == 0 {
		lines = append(lines, "  "+theme.Current.Muted.Render("No commands: the code window opens one plain shell."))
	}
	lines = append(lines, "", "  "+theme.Current.Muted.Render("{instances} → "+strings.ReplaceAll(fmt.Sprint(instancesOf(s)), " ", ",")+"   {ns_prefix} → "+orDefault(s.NSPrefix, "px4")))
	return strings.Join(lines, "\n"), "a add · enter edit · space autorun · x delete · esc back"
}

func instancesOf(s *Session) []int {
	var out []int
	for _, r := range s.Fleet {
		out = append(out, r.Instance)
	}
	sort.Ints(out)
	return out
}

func (a *app) updateCommands(msg tea.Msg) tea.Cmd {
	s := a.sess
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		var i int
		if _, err := fmt.Sscanf(a.hitAt(m.X, m.Y), "cmd:%d", &i); err == nil {
			a.cmdSel = i
		}
	case tea.KeyPressMsg:
		switch m.String() {
		case "up", "k":
			a.cmdSel = max(0, a.cmdSel-1)
		case "down", "j":
			a.cmdSel = min(len(s.Commands)-1, a.cmdSel+1)
		case "a":
			return a.pickCommand()
		case "enter", "e":
			if len(s.Commands) > 0 {
				return a.editCommand(a.cmdSel, s.Commands[a.cmdSel].Run)
			}
		case "space":
			if len(s.Commands) > 0 {
				s.Commands[a.cmdSel].Autorun = !s.Commands[a.cmdSel].Autorun
			}
		case "x", "delete", "backspace":
			if len(s.Commands) > 0 {
				s.Commands = append(s.Commands[:a.cmdSel], s.Commands[a.cmdSel+1:]...)
				a.validate()
			}
		case "esc", "q":
			a.screen = scrFleet
		}
	}
	return nil
}

func (a *app) pickCommand() tea.Cmd {
	choice := ""
	opts := []huh.Option[string]{huh.NewOption("Custom command…", "")}
	for _, l := range host.LaunchFiles(a.repo) {
		opts = append(opts, huh.NewOption(l, l))
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Start from").Description("launch files found in ws/src · / to filter").Options(opts...).Value(&choice).Height(12),
	).Title(theme.Current.I.Add + " Add command"))
	return a.openForm(f, scrCommands, func() tea.Cmd { return a.editCommand(-1, choice) })
}

func (a *app) editCommand(idx int, run string) tea.Cmd {
	autorun := idx >= 0 && a.sess.Commands[idx].Autorun
	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Command").Description("{instances} and {ns_prefix} are filled in from the fleet").Value(&run).Validate(noHash),
		huh.NewConfirm().Title("Run it automatically").Description("otherwise it is typed into the pane, ready for Enter").Value(&autorun),
	).Title("Code command"))
	return a.openForm(f, scrCommands, func() tea.Cmd {
		c := Command{Run: strings.TrimSpace(run), Autorun: autorun}
		if idx < 0 {
			a.sess.Commands = append(a.sess.Commands, c)
			a.cmdSel = len(a.sess.Commands) - 1
		} else {
			a.sess.Commands[idx] = c
		}
		a.validate()
		return nil
	})
}

// --- session options ---

func (a *app) editOptions() tea.Cmd {
	s := a.sess
	img, haveImg := a.image()
	name, image, world, gui := s.Name, s.Image, s.World, !s.Headless
	domain, prefix, speed, rmw := strconv.Itoa(s.DomainID), s.NSPrefix, ff(s.SpeedFactor), s.RMW
	rviz, rvizCfg, qgc, fox, bag := s.RViz, s.RVizConfig, s.QGC, s.Foxglove, s.Bag
	bagTopics, build, gpu, reset := strings.Join(s.BagTopics, ", "), s.Build, s.GPU, s.ResetParams
	windows, px4Params := append([]string(nil), s.Windows...), strings.Join(s.PX4Params, ", ")

	var imageField, worldField, rmwField huh.Field
	var imgOpts []huh.Option[string]
	hidden := 0
	for _, i := range a.images {
		if !i.HasPX4 {
			hidden++
			continue
		}
		imgOpts = append(imgOpts, huh.NewOption(fmt.Sprintf("PX4 %-10s %s  (gz %s)", i.Version(), i.Ref, orDefault(i.Gz, "?")), i.Ref))
	}
	if len(imgOpts) > 0 {
		desc := "local Docker images"
		if hidden > 0 {
			desc += fmt.Sprintf(" · %d without a PX4 build hidden", hidden)
		}
		imageField = huh.NewSelect[string]().Title("PX4 image").Description(desc).Options(imgOpts...).Value(&image).Height(min(8, max(3, len(imgOpts))))
	} else {
		imageField = huh.NewInput().Title("PX4 image").Description("no local PX4 SITL image found").Value(&image)
	}
	if haveImg && len(img.Worlds) > 0 {
		worldField = huh.NewSelect[string]().Title("World").Options(huh.NewOptions(img.Worlds...)...).Value(&world).Height(6)
	} else {
		worldField = huh.NewInput().Title("World").Value(&world).Validate(noHash)
	}
	rmwOpts := []huh.Option[string]{huh.NewOption("image default", "")}
	for _, r := range img.RMWs {
		rmwOpts = append(rmwOpts, huh.NewOption(r, r))
	}
	rmwField = huh.NewSelect[string]().Title("ROS 2 middleware (RMW)").Description("only the ones installed in the image").Options(rmwOpts...).Value(&rmw).Height(min(6, max(2, len(rmwOpts))))
	avail := func(ok bool) string {
		if !haveImg {
			return "image not probed"
		}
		if ok {
			return "available in the image"
		}
		return "NOT in the image: add it to the image first"
	}

	f := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Session name").Description("saved as config/sessions/<name>.yaml").Value(&name).Validate(matches(`[A-Za-z0-9_.-]+`)),
			imageField, worldField,
			huh.NewConfirm().Title("Gazebo GUI").Value(&gui),
			huh.NewInput().Title("ROS_DOMAIN_ID").Description("one value for container, agent, PX4 and every pane").Value(&domain).Validate(func(v string) error {
				if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 232 {
					return errors.New("0–232")
				}
				return nil
			}),
			huh.NewInput().Title("Namespace prefix").Description("instance N → /<prefix>_N").Value(&prefix).Validate(matches(`[A-Za-z][A-Za-z0-9_]*`)),
			huh.NewInput().Title("Sim speed factor").Description("PX4_SIM_SPEED_FACTOR (1 = real time)").Value(&speed).Validate(func(v string) error {
				if atof(v) <= 0 {
					return errors.New("> 0")
				}
				return nil
			}),
			rmwField,
			huh.NewConfirm().Title("RViz2").Description(avail(img.RViz)).Value(&rviz),
			huh.NewInput().Title("RViz config").Description("path inside the container, optional (e.g. /ws/src/.../x.rviz)").Value(&rvizCfg),
			huh.NewConfirm().Title("QGroundControl").Description("started on the host · set QGC_PATH in .env if not on PATH").Value(&qgc),
			huh.NewConfirm().Title("Foxglove bridge").Description(avail(img.Foxglove)).Value(&fox),
			huh.NewConfirm().Title("Record rosbag").Description("to ws/bags/<session>_<time>").Value(&bag),
			huh.NewInput().Title("Bag topics").Description("all, or comma separated topics").Value(&bagTopics),
			huh.NewMultiSelect[string]().Title("Windows").Options(
				huh.NewOption("PX4 logs", "px4"), huh.NewOption("XRCE agent log", "xrce"),
				huh.NewOption("mullet-core state machines", "core"), huh.NewOption("code", "code")).Value(&windows).Height(4),
			huh.NewConfirm().Title("colcon build before launch").Value(&build),
			huh.NewSelect[string]().Title("GPU").Options(
				huh.NewOption("auto (NVIDIA if nvidia-smi works)", "auto"), huh.NewOption("on", "on"), huh.NewOption("off", "off")).Value(&gpu).Height(3),
			huh.NewConfirm().Title("Reset persisted PX4 params").Value(&reset),
			huh.NewInput().Title("Session PX4 params").Description("NAME=VALUE, comma separated, applied to every robot").Value(&px4Params),
		).Title("Session options"),
	)
	return a.openForm(f, scrFleet, func() tea.Cmd {
		s.Name, s.Image, s.World, s.Headless = name, image, world, !gui
		s.DomainID, _ = strconv.Atoi(domain)
		s.NSPrefix, s.SpeedFactor, s.RMW = prefix, atof(speed), rmw
		s.RViz, s.RVizConfig, s.QGC, s.Foxglove, s.Bag = rviz, strings.TrimSpace(rvizCfg), qgc, fox, bag
		s.BagTopics = splitList(bagTopics)
		if len(s.BagTopics) == 0 {
			s.BagTopics = []string{"all"}
		}
		s.Build, s.GPU, s.ResetParams, s.Windows, s.PX4Params = build, gpu, reset, windows, splitList(px4Params)
		a.validate()
		return nil
	})
}
