package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/quay/claircore/libvuln/driver"
	"github.com/quay/claircore/libvuln/jsonblob"
	"github.com/quay/claircore/libvuln/updates"
	_ "github.com/quay/claircore/updater/defaults"
	"github.com/urfave/cli/v2"

	"github.com/quay/clair/v4/internal/httputil"
)

// ExportCmd is the "export-updaters" subcommand.
var ExportCmd = &cli.Command{
	Name:      "export-updaters",
	Action:    exportAction,
	Usage:     "run updaters and export results",
	ArgsUsage: "[out]",
	Flags: []cli.Flag{
		// Strict can be used to check that updaters still work.
		&cli.BoolFlag{
			Name:  "strict",
			Usage: "Return non-zero exit when updaters report errors.",
		},
	},
	Description: `Run configured exporters and export to a file.

   A configuration file is needed to run this command, see 'clairctl help'
   for how to specify one.`, // NB this has spaces, not tabs.
}

func exportAction(c *cli.Context) error {
	ctx := c.Context
	var out io.Writer

	// Setup the output file.
	args := c.Args()
	switch args.Len() {
	case 0:
		out = os.Stdout
	case 1:
		f, err := os.Create(args.First())
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	default:
		return errors.New("too many arguments (wanted at most one)")
	}

	// Read and process the config file.
	cfg, err := loadConfig(c.String("config"))
	if err != nil {
		return err
	}
	cfgs := make(map[string]driver.ConfigUnmarshaler, len(cfg.Updaters.Config))
	for name, node := range cfg.Updaters.Config {
		node := node
		cfgs[name] = func(v interface{}) error {
			b, err := json.Marshal(node)
			if err != nil {
				return err
			}
			return json.Unmarshal(b, v)
		}
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	cl, _, err := httputil.Client(httputil.RateLimiter(tr), &commonClaim, cfg)
	if err != nil {
		return err
	}

	store, err := jsonblob.New()
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Store(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	mgr, err := updates.NewManager(ctx, store, updates.NewLocalLockSource(), cl,
		updates.WithConfigs(cfgs),
		updates.WithEnabled(cfg.Updaters.Sets),
	)
	if err != nil {
		return err
	}

	if err := mgr.Run(ctx); err != nil {
		// Don't exit non-zero if we run into errors, unless the strict flag was
		// provided.
		code := 0
		if c.Bool("strict") {
			code = 1
		}
		return cli.Exit(err.Error(), code)
	}
	return nil
}
