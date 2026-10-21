// Command tkav lists the Traefik custom resources still
// served under the traefik.containo.us API group and exports their traefik.io
// equivalent, to be applied before migrating from Traefik v2 to v3.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/NEwa-05/traefik-k8s-apiversion/internal/migrate"
)

const usage = `Usage: tkav <command> [flags]

Find the Traefik objects still using the traefik.containo.us API group and
generate their traefik.io version, to apply before migrating to Traefik v3.

Commands:
  list     List traefik.containo.us objects and whether their traefik.io version exists
  export   Write the traefik.io version of the objects as YAML, one file per object
  version  Print the version of tkav

Run "tkav <command> -h" to see the flags of a command.
`

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()

	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return flag.ErrHelp
	}

	switch args[0] {
	case "list":
		return runList(ctx, args[1:], stdout, stderr)
	case "export":
		return runExport(ctx, args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "tkav", version)
		return nil
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// clusterFlags are the flags shared by all commands.
type clusterFlags struct {
	kubeconfig  string
	kubeContext string
	namespace   string
	pageSize    int64
}

func (c *clusterFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.kubeconfig, "kubeconfig", "", "path to the kubeconfig file (defaults to $KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&c.kubeContext, "context", "", "kubeconfig context to use (defaults to the current context)")
	fs.StringVar(&c.namespace, "namespace", "", "only process this namespace (defaults to all namespaces)")
	fs.StringVar(&c.namespace, "n", "", "shorthand for -namespace")
	fs.Int64Var(&c.pageSize, "page-size", migrate.DefaultPageSize, "number of objects fetched per list request")
}

func (c *clusterFlags) scan(ctx context.Context) ([]migrate.Item, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = c.kubeconfig
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules,
		&clientcmd.ConfigOverrides{CurrentContext: c.kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	disc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	scanner := migrate.Scanner{
		Discovery: disc,
		Dynamic:   dyn,
		Namespace: c.namespace,
		PageSize:  c.pageSize,
	}
	return scanner.Scan(ctx)
}

func runList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cluster clusterFlags
	cluster.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	items, err := cluster.scan(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintf(stdout, "No %s objects found.\n", migrate.OldGroup)
		return nil
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAMESPACE\tNAME\tTRAEFIK.IO STATUS")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", item.Resource.Kind, item.Source.GetNamespace(), item.Source.GetName(), item.Status)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	printSummary(stdout, items)
	return nil
}

func runExport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cluster clusterFlags
	cluster.register(fs)
	outputDir := fs.String("output-dir", "traefik-io-manifests", "directory where the manifests are written")
	all := fs.Bool("all", false, "also export objects whose traefik.io version is already up-to-date")
	overwrite := fs.Bool("overwrite", false, "write into the output directory even if it is not empty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*overwrite {
		entries, err := os.ReadDir(*outputDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("output directory %q is not empty, use -overwrite to write into it anyway", *outputDir)
		}
	}

	items, err := cluster.scan(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintf(stdout, "No %s objects found.\n", migrate.OldGroup)
		return nil
	}

	var written, skipped int
	for _, item := range items {
		if item.Status == migrate.StatusUpToDate && !*all {
			skipped++
			continue
		}

		path, warnings, err := migrate.WriteManifest(*outputDir, item.Converted)
		if err != nil {
			return err
		}
		written++
		fmt.Fprintf(stdout, "%s\t(%s)\n", path, item.Status)

		for _, warning := range append(item.Warnings, warnings...) {
			fmt.Fprintf(stderr, "warning: %s %s/%s: %s\n", item.Resource.Kind, item.Source.GetNamespace(), item.Source.GetName(), warning)
		}
	}

	printSummary(stdout, items)
	fmt.Fprintf(stdout, "\nWrote %d manifest(s) to %s", written, *outputDir)
	if skipped > 0 {
		fmt.Fprintf(stdout, ", skipped %d already up-to-date (use -all to include them)", skipped)
	}
	fmt.Fprintln(stdout, ".")
	if written > 0 {
		fmt.Fprintf(stdout, "Review them, then apply with:\n  kubectl apply --server-side -R -f %s\n", *outputDir)
	}
	return nil
}

func printSummary(w io.Writer, items []migrate.Item) {
	counts := make(map[migrate.Status]int)
	for _, item := range items {
		counts[item.Status]++
	}

	fmt.Fprintf(w, "\n%d %s object(s): %d missing, %d out-of-sync, %d up-to-date, %d without traefik.io CRD\n",
		len(items), migrate.OldGroup,
		counts[migrate.StatusMissing], counts[migrate.StatusOutOfSync],
		counts[migrate.StatusUpToDate], counts[migrate.StatusNoCRD])

	if counts[migrate.StatusNoCRD] > 0 {
		fmt.Fprintf(w, "Install the %s CRDs (Traefik v2.10+) before applying the exported manifests.\n", migrate.NewGroup)
	}
}
