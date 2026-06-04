package onec

import (
	"context"
	"fmt"
	"time"
)

// Downloader orchestrates the 3-step 1C/PIM data download.
// All fields are interfaces — no concrete types from storage or API packages.
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates a Downloader with the given source, writer, and options.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the 3-step download: Goods → Prices → PIM.
// Each step is non-fatal — errors are collected but subsequent steps still execute.
// Returns the result with counts and any step errors.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Clean tables if requested
	if d.opts.Clean && !d.opts.DryRun {
		if err := d.writer.CleanAll(ctx); err != nil {
			return nil, fmt.Errorf("clean: %w", err)
		}
		d.progress("Tables cleaned")
	}

	// Step 1/3: Goods + SKUs + Dimensions
	d.stepGoods(ctx, result)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	// Step 2/3: Prices
	d.stepPrices(ctx, result)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	// Step 3/3: PIM
	d.stepPIM(ctx, result)

	result.Duration = time.Since(start)

	if result.HasErrors() {
		return result, result.StepErrors[0].Err
	}
	return result, nil
}

// stepGoods fetches and saves goods, SKUs, and dimensions.
func (d *Downloader) stepGoods(ctx context.Context, result *DownloadResult) {
	goods, skus, dims, err := d.source.FetchGoods(ctx, d.opts.GoodsURL)
	if err != nil {
		result.StepErrors = append(result.StepErrors, StepError{Step: "goods", Err: err})
		d.progress(fmt.Sprintf("Step 1/3: goods failed: %v", err))
		return
	}

	if !d.opts.DryRun {
		n, err := d.writer.SaveGoods(ctx, goods)
		if err != nil {
			result.StepErrors = append(result.StepErrors, StepError{Step: "goods", Err: fmt.Errorf("save goods: %w", err)})
		}
		result.GoodsCount = n

		sn, err := d.writer.SaveSKUs(ctx, skus)
		if err != nil {
			result.StepErrors = append(result.StepErrors, StepError{Step: "goods", Err: fmt.Errorf("save SKUs: %w", err)})
		}
		result.SKUCount = sn

		dn, err := d.writer.SaveDimensions(ctx, dims)
		if err != nil {
			result.StepErrors = append(result.StepErrors, StepError{Step: "goods", Err: fmt.Errorf("save dimensions: %w", err)})
		}
		result.DimensionCount = dn
	} else {
		result.GoodsCount = len(goods)
		result.SKUCount = len(skus)
		result.DimensionCount = len(dims)
	}

	d.progress(fmt.Sprintf("Step 1/3: %d goods, %d SKUs, %d dimensions",
		result.GoodsCount, result.SKUCount, result.DimensionCount))
}

// stepPrices fetches and saves price rows.
func (d *Downloader) stepPrices(ctx context.Context, result *DownloadResult) {
	prices, err := d.source.FetchPrices(ctx, d.opts.PricesURL)
	if err != nil {
		result.StepErrors = append(result.StepErrors, StepError{Step: "prices", Err: err})
		d.progress(fmt.Sprintf("Step 2/3: prices failed: %v", err))
		return
	}

	if !d.opts.DryRun {
		n, err := d.writer.SaveOneCPrices(ctx, prices, d.opts.SnapshotDate)
		if err != nil {
			result.StepErrors = append(result.StepErrors, StepError{Step: "prices", Err: fmt.Errorf("save prices: %w", err)})
		}
		result.PriceCount = n
	} else {
		result.PriceCount = len(prices)
	}

	d.progress(fmt.Sprintf("Step 2/3: %d price rows", result.PriceCount))
}

// stepPIM fetches and saves PIM goods.
func (d *Downloader) stepPIM(ctx context.Context, result *DownloadResult) {
	items, err := d.source.FetchPIMGoods(ctx, d.opts.PIMURL)
	if err != nil {
		result.StepErrors = append(result.StepErrors, StepError{Step: "pim", Err: err})
		d.progress(fmt.Sprintf("Step 3/3: PIM failed: %v", err))
		return
	}

	if !d.opts.DryRun {
		n, err := d.writer.SavePIMGoods(ctx, items)
		if err != nil {
			result.StepErrors = append(result.StepErrors, StepError{Step: "pim", Err: fmt.Errorf("save PIM: %w", err)})
		}
		result.PIMCount = n
	} else {
		result.PIMCount = len(items)
	}

	d.progress(fmt.Sprintf("Step 3/3: %d PIM goods", result.PIMCount))
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(msg string) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(msg)
	}
}
