# Diesel tracker

A heating-oil tank, read with a dipstick: how much is left, what it is worth, how fast it goes with the heating on and off, how warm it is inside and out, and when to order. It is a [solution](../../docs/solutions.md): a list, four workflows and an [app](../../docs/apps.md), the dashboard, installed together into a workspace. The [use case analysis](../../docs/plans/use-case-diesel-analysis.md) explains the design.

## Install

1. **Apply the solution** to a workspace that exists, as someone who manages it and has `solution.manage`:

   ```sh
   papergo solution plan ./samples/diesel --workspace Home     # what it would do
   papergo solution apply ./samples/diesel --workspace Home
   ```

   This creates, in that workspace:
   - the **Diesel** list, with four content types:
     - **Reading**: when, the stick reading from the mouth in cm, and the litres, fill and check the levels workflow works out;
     - **Purchase**: when, litres, total price in EUR, price per litre, supplier;
     - **Heating**: when the heating went on or off;
     - **Temperature**: the date and time, and the indoor and outdoor °C, either or both;
   - five saved views: Readings, Purchases, Heating, Temperatures and Needs a look;
   - the four workflows below;
   - the **Diesel dashboard** app, published, with its parameters set to the **Diesel** list and its **Reading**, **Purchase**, **Heating** and **Temperature** types. It appears in the sidebar under the list.

   Whoever applies it becomes the author of the workflows and the app. The **Diesel** list is created with search off: readings and temperatures are found by date on the dashboard and in the views, not by searching their values, and a sensor filing a temperature every hour would otherwise index each one.

   To use a list you already have, bind it: `--bind tank=<its name>`. The solution adds its content types and views there and leaves the list's name and settings.

To try it with real data, run **Load diesel sample data** once from **Workspace settings → Workflows**. It files eighteen months of a 750 L tank into the empty list: four fills, nineteen readings, three heating changes and eighteen temperatures taken with the readings. The tank's figures are real; the temperatures are illustrative.

## The workflows

| Workflow | Starts on | Does |
| --- | --- | --- |
| **Diesel levels** | a reading or purchase created or changed; **Run once** for owners | Converts the stick reading to litres and fill. Marks a reading `check` when it is higher than the reading before plus what was bought between, unless it is marked `reviewed`. Works out each purchase's price per litre and checks the reading after it again. **Run once** recalculates every reading. |
| **Diesel measuring reminder** | every morning at 08:00; a new reading; **Run once** | Asks the assignees to measure when the last reading is older than `every_days`. The task's form is the reading, and a reading filed any other way closes it. |
| **Diesel refill** | a reading converted below `threshold_l`; a new purchase | Asks the buyers to fill the tank. The task's form is the purchase. A later low reading updates the open task to its level, and a purchase closes it. |
| **Load diesel sample data** | **Run once** for owners | Files the sample history into an empty list. |

## Settings

The solution asks two settings when it is applied, and keeps them:

| Setting | Default | Reaches |
| --- | --- | --- |
| `capacity_l` | 750 | the Diesel levels workflow and the dashboard |
| `time_zone` | the organization's time zone | the measuring reminder |

```sh
papergo solution apply ./samples/diesel --workspace Home --set capacity_l=1000
```

The rest are parameters of the workflows, in `workflows/*.json` or **Workspace settings → Workflows**:

| Workflow | Parameter | Ships as |
| --- | --- | --- |
| Diesel levels | `useful_height_cm`, `stick_length_cm` | 125 and 200: with 750 L, 6 L a centimetre and a 75 cm margin above the diesel |
| | `measure` | `from_top`: the stick measures from the mouth down to the diesel; `depth` reads its wet length |
| | `orientation` | `upright` or `horizontal` (a cylinder on its side) |
| | `tolerance_l` | 5 |
| Diesel measuring reminder | `every_days`, `assignees` | 15, `["role:owners"]`; set who measures |
| Diesel refill | `threshold_l`, `buyers` | 200, `["role:owners"]` |

- **The app's parameters:** the tank's capacity, when to order (200 L), the reserve (25 %), the litres a day with the heating off (1.5), on (4.5) and on in the coldest months (7.5), and those months (December to February).
- **Currency:** prices are kept in EUR (`diesel_total_price`). Change the field's currency in the catalog before the first purchase to use another.

## The dashboard

- **The tank:**
  - The last reading, less what the season burns each day since. After the last recorded heating change, the heating is assumed to go on and off on its usual days, the average of the recorded ones.
  - Shown as a gauge, beside the litres and what they are worth at the last price per litre.
- **Autonomy and forecast:** the date to order, and the date the reserve is reached, counted forward a day at a time at the rate of each day's season. The two are close when the order point (200 L) is close to the reserve (25 %, 187.5 L), but they are different thresholds.
- **Pace of consumption:** today's cost a day and rate, and the costs of deep winter and summer.
- **Charts:**
  - **The level over time:** the heating seasons shaded, fills marked, a tick every three months, the reserve as a dashed line, and the forecast to the next order.
  - **The price per litre of each fill:** its axis fits the prices, so a rise shows.
  - **The cost of the diesel used each month:** what was burnt between readings, valued at the price of the fill it came from.
- **Temperature and heating:** the last indoor and outdoor temperatures, and each day's averages over time against the heating seasons, worked out by the server in your time zone, so a sensor filing a temperature every hour keeps the screen as fast as a few readings do. **+ Temperatura** on the card records them at a date and time, now unless changed, since temperatures change through the day; the ± button turns a typed temperature below zero, which phones' decimal keyboards cannot. Each figure says when it was taken, and an entry may hold just one of the two. Tapping a point reads that day's average.
- **Consumption:** what each season actually burnt between readings, beside the rates the forecast uses, so the rates can be tuned, and the season's average outdoor temperature.
- **Actions:** **+ Medición**, **+ Llenado** and the heating button open a dialog.
  - Typing a stick reading shows what it means before it is saved, worked out with the Diesel levels workflow's settings.
  - Decimals are typed in your number format, so `86,8` is 86.8 cm where the decimal separator is a comma, with as many decimals as the field keeps: one for centimetres, litres and degrees, two for prices.
  - Dates are dates in your time zone: today keeps the current time, so a reading before and one after a fill stay in order; another day is noon.
- **Lists:** the last five readings and fills, with the supplier, and the whole history in the list.
- **Tasks and checks:** the measuring and refill tasks waiting for you, and the readings that need a look.

Converting readings again after the tank's settings change is **Run once** of Diesel levels, in Workspace settings → Workflows. The dashboard itself reads again whenever something changes.

## Sources

[`solution.json`](solution.json) (fields, content types, the list and its views, the settings), the workflow scripts with their settings in [`workflows/`](workflows), and the app in [`apps/`](apps) are the editable sources: the folder is the solution.
- `papergo solution pack ./samples/diesel` checks it and writes `diesel-1.0.0.zip`.
- The [solutions test](../../internal/solutions/lifecycle_test.go) installs it beside Receipts in one workspace.
- The [diesel test](../../internal/scriptrun/diesel_test.go) runs the workflows end to end on the sample data.

The sample has its names in Spanish too, in [`locales/es.json`](locales/es.json): a Spanish organization gets them, and `--language es` installs them anywhere.
