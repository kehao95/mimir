// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/grafana/dskit/multierror"
	"github.com/pkg/errors"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/grafana/mimir/pkg/mimirtool/config"
)

// ConfigCommand works with the mimir configuration parameters (YAML files and CLI flags)
type ConfigCommand struct {
	yamlFile  string
	flagsFile string

	outYAMLFile    string
	outFlagsFile   string
	outNoticesFile string
}

// Register rule related commands and flags with the kingpin application
func (c *ConfigCommand) Register(app *kingpin.Application, _ EnvVarNames) {
	configCmd := app.Command("config", "Work with Grafana Mimir configuration.")

	convertCmd := configCmd.
		Command("convert", "Convert a configuration file from Cortex v1.11.0 to Grafana Mimir and output it to stdout").
		Action(c.convertConfig)

	convertCmd.Flag("yaml-file", "The YAML configuration file to convert.").StringVar(&c.yamlFile)
	convertCmd.Flag("flags-file", "New-line-delimited list of CLI flags to convert.").StringVar(&c.flagsFile)
	convertCmd.Flag("yaml-out", "Location to output the converted YAML configuration to. Default STDOUT").StringVar(&c.outYAMLFile)
	convertCmd.Flag("flags-out", "Location to output list of converted CLI flags to. Default STDOUT").StringVar(&c.outFlagsFile)
}

func (c *ConfigCommand) convertConfig(_ *kingpin.ParseContext) error {
	yamlContents, flagsFlags, err := c.prepareInputs()
	if err != nil {
		return err
	}

	convertedYAML, flagsFlags, notices, err := config.Convert(yamlContents, flagsFlags, config.CortexToMimirMapper, config.DefaultCortexConfig, config.DefaultMimirConfig)
	if err != nil {
		return errors.Wrap(err, "could not convert configuration")
	}

	return c.output(convertedYAML, flagsFlags, notices)
}

func (c *ConfigCommand) prepareInputs() ([]byte, []string, error) {
	var (
		yamlContents []byte
		flags        []string
		err          error
	)

	if c.flagsFile == "" && c.yamlFile == "" {
		return nil, nil, errors.New("provide at least one of --config-file or --flags-file")
	}
	yamlContents, err = os.ReadFile(c.yamlFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, errors.Wrap(err, "could not read config-file")
	}

	flagsContents, err := os.ReadFile(c.flagsFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, errors.Wrap(err, "could not read flags-file")
	}
	if len(flagsContents) > 1 {
		flags = strings.Split(string(flagsContents), "\n")
	}
	return yamlContents, flags, nil
}

func (c *ConfigCommand) output(yamlContents []byte, flags []string, notices config.ConversionNotices) error {
	openFile := func(path string, defaultWriter io.Writer) (io.Writer, func(), error) {
		if path == "" {
			return defaultWriter, func() {}, nil
		}
		outWriter, err := os.OpenFile(path, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return nil, nil, errors.Wrap(err, "could not open "+path)
		}
		closeFn := func() {
			err := outWriter.Close()
			if err != nil {
				_, _ = fmt.Fprint(os.Stderr, err)
			}
		}
		return outWriter, closeFn, nil
	}

	outYAMLWriter, closeFile, err := openFile(c.outYAMLFile, os.Stdout)
	if err != nil {
		return err
	}
	defer closeFile()

	outFlagsWriter, closeFile, err := openFile(c.outFlagsFile, os.Stdout)
	if err != nil {
		return err
	}
	defer closeFile()

	outNoticesWriter, closeFile, err := openFile(c.outNoticesFile, os.Stderr)
	if err != nil {
		return err
	}
	defer closeFile()

	_, err = fmt.Fprintln(outYAMLWriter, string(yamlContents))
	_, err2 := fmt.Fprintln(outFlagsWriter, strings.Join(flags, "\n"))
	err3 := c.writeNotices(notices, outNoticesWriter)

	return multierror.New(err, err2, err3).Err()
}

func (c *ConfigCommand) writeNotices(notices config.ConversionNotices, w io.Writer) error {
	noticesOut := bytes.Buffer{}
	for _, p := range notices.RemovedParameters {
		_, _ = noticesOut.WriteString(fmt.Sprintf("field %s is no longer supported\n", p))
	}
	for _, f := range notices.RemovedCLIFlags {
		_, _ = noticesOut.WriteString(fmt.Sprintf("flag -%s is no longer supported\n", f))
	}
	for _, d := range notices.ChangedDefaults {
		oldDefault, newDefault := d.OldDefault, d.NewDefault
		if newDefault == "" {
			newDefault = "<empty>"
		}
		if oldDefault == "" {
			oldDefault = "<empty>"
		}
		_, _ = noticesOut.WriteString(fmt.Sprintf("using a new default for %s: %v (used to be %v)\n", d.Path, newDefault, oldDefault))
	}

	_, err := noticesOut.WriteTo(w)
	return err
}