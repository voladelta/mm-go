Inventory skew pushes both quotes inward/outward based on
your current position; trend skew nudges quotes against the
prevailing move. To tune them for PnL, treat it like any other
risk/parameter search:

  - InventorySkewK (default 0.6) controls how aggressively you
    offload inventory. Higher values mean the book leans harder
    —if you’re long, bids drop and asks drop even more so you
    sell out faster, which cuts inventory risk but can reduce
    spread capture when positions are small. Start at 0.4–0.8
    and watch how inventory variance and adverse excursions
    change; raise it if you’re carrying too much inventory into
    reversals, lower it if you’re missing fills because your
    quotes move too far from mid.
  - TrendSkewK (default 0.2) sets how much you lean quotes
    against the detected trend. Bigger values move both quotes
    opposite the trend direction: in an uptrend you shade
    the bid lower (to avoid buying into strength) and the
    ask lower (to sell into strength sooner). Higher values
    mitigate adverse selection during fast moves but also reduce
    participation when you actually want to go with the move.
    Sweep 0.0–0.4 and monitor how many fills happen during trend
    periods versus choppy ones.

To find the sweet spot:

  - Run a grid search/backtest across reasonable ranges (e.g.,
    InventorySkewK = {0.2, 0.4, 0.6, 0.8}, TrendSkewK = {0.0,
    0.1, 0.2, 0.3, 0.4}) on recent data with mm_mei_v2.go. Track
    PnL, Sharpe, max drawdown, inventory exposure, and fill
    counts. The best PnL that also keeps inventory variance
    within acceptable limits is your target.
  - Inspect edge cases: trending markets vs. mean-reverting
    segments. You might prefer slightly higher TrendSkewK in
    volatile-trending markets and lower values in heaver chop.
  - Once you narrow the range, run longer walk-forward tests to
    ensure the choice generalizes.

If you plan to vary them intraday, consider tying them to
volatility/efficiency readings—e.g., increase TrendSkewK when
normalized efficiency is high, or scale InventorySkewK when
inventory magnitude crosses thresholds.
