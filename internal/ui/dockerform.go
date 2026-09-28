package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/icortesb/lazykuma/internal/core"
	"github.com/icortesb/lazykuma/internal/kuma"
)

// dockerRow is a row of the Docker host form, in its order down the screen.
type dockerRow int

const (
	drName dockerRow = iota
	drType
	drDaemon
	dockerRows
)

// dockerTypes are how Kuma reaches a daemon, in the order the form cycles.
var dockerTypes = []string{"socket", "tcp"}

// dockerPlaceholders are what the daemon field suggests for each type.
var dockerPlaceholders = []string{"/var/run/docker.sock", "tcp://10.0.0.7:2375"}

// dockerForm makes a Docker host, or edits one, and tests it unsaved. Its
// type is picked, not typed, so it draws its rows itself.
type dockerForm struct {
	host         kuma.DockerHost // as the form opened it, ID 0 for a new one
	name, daemon textinputModel
	typ          int // in dockerTypes
	row          dockerRow
	err          string
	// pending is set while Kuma saves: a second enter would make a second
	// host, for a new one.
	pending bool
	// testing is set while Kuma tries the form's values, which can take it
	// six seconds.
	testing bool
}

func newDockerForm(h kuma.DockerHost) dockerForm {
	f := dockerForm{
		host:   h,
		name:   newField("name     ", "nas"),
		daemon: newField("daemon   ", ""),
	}
	for i, t := range dockerTypes {
		if t == h.Type {
			f.typ = i
		}
	}
	f.name.Width, f.daemon.Width = proxyFieldWidth, proxyFieldWidth
	f.daemon.Placeholder = dockerPlaceholders[f.typ]
	f.name.SetValue(h.Name)
	f.daemon.SetValue(h.Daemon)
	f, _ = f.focus()
	return f
}

// field is the text field of a row, nil for the type.
func (f *dockerForm) field(r dockerRow) *textinputModel {
	switch r {
	case drName:
		return &f.name
	case drDaemon:
		return &f.daemon
	}
	return nil
}

// focus puts the text cursor in the row's field, if it has one.
func (f dockerForm) focus() (dockerForm, tea.Cmd) {
	f.name.Blur()
	f.daemon.Blur()
	if fld := f.field(f.row); fld != nil {
		return f, fld.Focus()
	}
	return f, nil
}

// Update handles a key. Enter saves from any row, esc cancels; t on the
// type row, or ctrl+t anywhere, tests. Letters typed into a text field are
// always text, so h, l and t do nothing else there.
func (f dockerForm) Update(msg tea.Msg) (dockerForm, formAction, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if !isKey {
		// The cursor's blink.
		var cmd tea.Cmd
		if fld := f.field(f.row); fld != nil {
			*fld, cmd = fld.Update(msg)
		}
		return f, formNone, cmd
	}
	if k.Type != tea.KeyRunes {
		switch {
		case k.Type == tea.KeyEnter:
			if f.pending {
				return f, formNone, nil
			}
			return f, formSubmit, nil
		case k.Type == tea.KeyCtrlT:
			return f, formTest, nil
		case key.Matches(k, keys.Back):
			return f, formCancel, nil
		case k.Type == tea.KeyTab, k.Type == tea.KeyDown:
			f.row = (f.row + 1) % dockerRows
			f, cmd := f.focus()
			return f, formNone, cmd
		case k.Type == tea.KeyShiftTab, k.Type == tea.KeyUp:
			f.row = (f.row + dockerRows - 1) % dockerRows
			f, cmd := f.focus()
			return f, formNone, cmd
		}
	}
	if fld := f.field(f.row); fld != nil {
		var cmd tea.Cmd
		*fld, cmd = typeInto(*fld, msg)
		return f, formNone, cmd
	}
	n := len(dockerTypes)
	switch k.String() {
	case "right", "l":
		f.typ = (f.typ + 1) % n
	case "left", "h":
		f.typ = (f.typ + n - 1) % n
	case "t":
		return f, formTest, nil
	}
	f.daemon.Placeholder = dockerPlaceholders[f.typ]
	return f, formNone, nil
}

// Values are the host as the form holds it.
func (f dockerForm) Values() (kuma.DockerHost, error) {
	h := kuma.DockerHost{ID: f.host.ID, Type: dockerTypes[f.typ]}
	h.Name = strings.TrimSpace(f.name.Value())
	if h.Name == "" {
		return kuma.DockerHost{}, errors.New("the name is empty")
	}
	h.Daemon = strings.TrimSpace(f.daemon.Value())
	if h.Daemon == "" {
		return kuma.DockerHost{}, errors.New("the daemon is empty")
	}
	return h, nil
}

func (f dockerForm) View() string {
	var b strings.Builder
	title := "New Docker host"
	if f.host.ID != 0 {
		title = "Edit Docker host " + truncate(f.host.Name, 40)
	}
	b.WriteString(styleHeading.Render(title) + "\n")
	b.WriteString(styleLabel.Render("docker monitors name it by its id") + "\n\n")
	b.WriteString(" " + f.name.View() + "\n")
	typ := "type     ‹ " + dockerTypes[f.typ] + " ›"
	if f.row == drType {
		b.WriteString(styleRow.Render(" "+typ+" ") + "\n")
	} else {
		b.WriteString(styleValue.Render(" "+typ) + "\n")
	}
	b.WriteString(" " + f.daemon.View() + "\n")
	switch {
	case f.testing:
		b.WriteString("\n" + styleLabel.Render("testing…") + "\n")
	case f.pending:
		b.WriteString("\n" + styleLabel.Render("saving…") + "\n")
	}
	if f.err != "" {
		b.WriteString("\n" + styleErr.Render(truncate(f.err, 58)) + "\n")
	}
	// Two spaces between the keys, so the line fits 60 columns.
	b.WriteString("\n" + styleFooter.Render(styleKey.Render("tab")+" next  "+styleKey.Render("←/→")+" type  "+
		styleKey.Render("ctrl+t")+" test  "+styleKey.Render("enter")+" save  "+styleKey.Render("esc")+" cancel"))
	return b.String()
}

// dockerTest is a Docker host test on its way: the instance and host it is
// for, and the host's id when it was asked from the list, whose row shows it.
// Kuma can take six seconds; one runs at a time.
type dockerTest struct {
	instance, name string
	listID         int
}

func (t dockerTest) running() bool { return t.instance != "" }

// dockerTested is Kuma's answer to a Docker host test.
type dockerTested struct {
	instance, name, msg string
	err                 error
}

func testDocker(in *core.Instance, h kuma.DockerHost) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		msg, err := in.TestDockerHost(ctx, h)
		return dockerTested{instance: in.Name(), name: h.Name, msg: msg, err: err}
	}
}

// flash is the answer as it shows, longer for a failure, which has more to
// read.
func (t dockerTested) flash() tea.Cmd {
	if t.err != nil {
		return flashFor("Docker "+t.name+": "+kuma.Brief(t.err), 8*time.Second)
	}
	return flashFor("Docker "+t.name+": "+t.msg, 5*time.Second)
}

func saveDockerHost(in *core.Instance, h kuma.DockerHost) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		action := "saved"
		if h.ID == 0 {
			action = "created"
		}
		_, err := in.SaveDockerHost(ctx, h)
		return actionDone{name: in.Name(), action: action, mon: "Docker host " + h.Name, err: err}
	}
}

func deleteDockerHost(in *core.Instance, h kuma.DockerHost) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDone{name: in.Name(), action: "deleted", mon: "Docker host " + h.Name, err: in.DeleteDockerHost(ctx, h.ID)}
	}
}
