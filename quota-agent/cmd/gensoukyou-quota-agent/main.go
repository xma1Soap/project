package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
	"github.com/xma1Soap/project/quota-agent/internal/engine"
	"github.com/xma1Soap/project/quota-agent/internal/lock"
	"github.com/xma1Soap/project/quota-agent/internal/newapi"
	"github.com/xma1Soap/project/quota-agent/internal/state"
	"github.com/xma1Soap/project/quota-agent/internal/wizard"
)

var version = "dev"

const productionConfirmation = "gensoukyou.xyz"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "run":
		return runAgent(args[1:], false)
	case "once":
		return runAgent(args[1:], true)
	case "check-config":
		return checkConfig(args[1:])
	case "wizard":
		return runWizard(args[1:])
	case "status":
		return showStatus(args[1:])
	case "reset-pool":
		return resetPool(args[1:])
	case "version", "--version", "-version":
		fmt.Println(version)
		return nil
	default:
		return usageError()
	}
}

func runAgent(args []string, once bool) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/gensoukyou-quota-agent/config.json", "config path")
	confirmLive := flags.Bool("confirm-live-actions", false, "allow route mutations when config dry_run=false")
	confirmHost := flags.String("confirm-production-host", "", "must equal gensoukyou.xyz for live actions")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	effectiveLive := !cfg.DryRun && *confirmLive && *confirmHost == productionConfirmation
	if !cfg.DryRun && !effectiveLive {
		fmt.Fprintln(os.Stderr, "warning: live mode requested but confirmation gates are incomplete; forcing dry-run")
	}
	accessToken := strings.TrimSpace(os.Getenv(cfg.NewAPI.AccessTokenEnv))
	if accessToken == "" {
		return fmt.Errorf("required token environment variable %s is empty", cfg.NewAPI.AccessTokenEnv)
	}
	client, err := newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.UserID, accessToken, time.Duration(cfg.NewAPI.TimeoutSeconds)*time.Second)
	if err != nil {
		return err
	}
	runtime, err := state.Load(cfg.StatePath)
	if err != nil {
		return err
	}
	instanceLock, err := lock.Acquire(cfg.LockPath)
	if err != nil {
		return err
	}
	defer instanceLock.Close()
	controller := &engine.Engine{
		Config: cfg, Gateway: client, State: runtime, Live: effectiveLive,
		Persist: func(value state.State) error { return state.Save(cfg.StatePath, value) },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	encoder := json.NewEncoder(os.Stdout)
	execute := func() error {
		events, runErr := controller.RunOnce(ctx, time.Now())
		if runErr != nil {
			return runErr
		}
		for _, item := range events {
			if err := encoder.Encode(item); err != nil {
				return err
			}
		}
		return nil
	}
	if err := execute(); once {
		return err
	} else if err != nil {
		if encodeErr := encoder.Encode(engine.Event{Time: state.NowString(time.Now()), Action: "error", Reason: "poll_failed"}); encodeErr != nil {
			return encodeErr
		}
	}
	ticker := time.NewTicker(time.Duration(cfg.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := execute(); err != nil {
				if encodeErr := encoder.Encode(engine.Event{Time: state.NowString(time.Now()), Action: "error", Reason: "poll_failed"}); encodeErr != nil {
					return encodeErr
				}
			}
		}
	}
}

func checkConfig(args []string) error {
	flags := flag.NewFlagSet("check-config", flag.ContinueOnError)
	path := flags.String("config", "/etc/gensoukyou-quota-agent/config.json", "config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	monitored := 0
	for _, policy := range cfg.Channels {
		if policy.QuotaMode != config.QuotaIgnore {
			monitored++
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "dry_run": cfg.DryRun, "policies": len(cfg.Channels), "monitored": monitored})
}

func runWizard(args []string) error {
	flags := flag.NewFlagSet("wizard", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8765", "loopback listen address")
	output := flags.String("output", "/etc/gensoukyou-quota-agent/config.json", "config output path")
	exitAfterSave := flags.Bool("exit-after-save", false, "stop the wizard after a valid config is saved")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return wizard.Server{Listen: *listen, Output: *output, Out: os.Stdout, ExitAfterSave: *exitAfterSave}.Serve(ctx)
}

func showStatus(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	path := flags.String("state", "/var/lib/gensoukyou-quota-agent/state.json", "state path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	value, err := state.Load(*path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func resetPool(args []string) error {
	flags := flag.NewFlagSet("reset-pool", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/gensoukyou-quota-agent/config.json", "config path")
	poolName := flags.String("pool", "", "quota pool name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*poolName) == "" {
		return errors.New("--pool is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	instanceLock, err := lock.Acquire(cfg.LockPath)
	if err != nil {
		return fmt.Errorf("stop the quota-agent service before reset-pool: %w", err)
	}
	defer instanceLock.Close()
	value, err := state.Load(cfg.StatePath)
	if err != nil {
		return err
	}
	pool := value.Pools[*poolName]
	if pool == nil || pool.Phase != "exhausted" {
		return errors.New("pool is not in exhausted state")
	}
	pool.ReenableAt = state.NowString(time.Now())
	if err := state.Save(cfg.StatePath, value); err != nil {
		return err
	}
	fmt.Println("reset scheduled; the agent will probe owned routes before enabling them")
	return nil
}

func usageError() error {
	return errors.New("usage: gensoukyou-quota-agent <run|once|check-config|wizard|status|reset-pool|version>")
}
