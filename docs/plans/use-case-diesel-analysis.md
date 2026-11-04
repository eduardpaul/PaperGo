# Use case analysis: diesel tracker

Status: implemented as the complete sample of [workspace apps](workspace-apps.md) phase 0, in [`samples/diesel`](../../samples/diesel/README.md). This document covers what the tracker needs and why it is built the way it is; the sample's README says how to install and set it up.

## The problem

A house keeps heating oil in a 750 L tank. Three things happen:

1. **Measure.** Someone puts a 2 m stick down the filling mouth and reads where the diesel starts.
2. **Buy.** Someone buys some litres for a price on a date, and the tank is filled.
3. **Heat.** The heating goes on in autumn and off in spring; without it the tank only feeds hot water, and lasts several times longer.

They want one screen that says how much is left, what it is worth, how fast it goes in each season, and when to order.

## User stories

| # | As | I want to | So that |
| --- | --- | --- | --- |
| D1 | anyone in the workspace | enter a stick reading in cm, on a phone, with my decimal comma | recording it is quicker than not recording it |
| D2 | anyone | see the litres and percentage of that reading at once | I don't have to do the arithmetic |
| D3 | anyone | record a purchase: litres, total price, date, optionally the supplier | spend and price per litre are tracked |
| D4 | anyone | record when the heating goes on or off | consumption and forecasts follow the season |
| D5 | the owner | see today's estimated level, litres and their value, the days to the reserve, the date to order and the daily cost | I know when to buy and what heating costs us |
| D6 | the owner | see the level over time against the heating seasons, the price per litre and the monthly cost | I understand where the diesel goes |
| D7 | the owner | set the tank's size and how the stick is read, and fix it later | every reading, old ones included, converts correctly |
| D8 | the owner | be told when a reading makes no sense, and say it is right | a typo or a forgotten purchase does not corrupt the figures |
| D9 | anyone | correct an entry in the ordinary list screen | the app is not the only way in |
| D10 | the person who measures | be asked to measure when 15 days have passed, and answer with the reading itself | readings happen without anyone keeping a calendar |
| D11 | the people who buy | be asked to fill the tank when it falls below 200 L, and answer with the purchase | it is filled before it runs dry, and the purchase is recorded |

## From the stick to litres

The tank holds diesel over a useful height of 125 cm. The filling mouth is 200 cm above the bottom, so there are 75 cm of margin above the diesel. The stick measures from the mouth down to the diesel:

```
wet cm  = 200 − stick cm                  (within 0 … 125)
litres  = wet cm × 750 / 125              (6 L a centimetre)
fill %  = litres / 750 × 100
```

A stick reading of 86.8 cm leaves 113.2 cm of diesel: 679.2 L, 90.6 %. The workflow takes the capacity, the useful height, the stick's length and the way it is read (`from_top`, or `depth` for the wet length) as parameters. A tank lying on its side is a parameter too (`orientation: horizontal`): its litres follow the circular segment, `C × (θ − sin θ) / 2π` with `θ = 2·acos(1 − 2h/D)`.

## Data model

One list, **Diesel**, with three content types: **Reading**, **Purchase** and **Heating**. One list keeps the history in one place, and the app reads each type with one live query. Every field and type key carries the `diesel_` prefix, because catalog keys are organization-wide.

| Content type | Field | Type | Notes |
| --- | --- | --- | --- |
| **Reading** | `diesel_measured_at` | datetime, required, indexed | defaults to now in the app |
| | `diesel_stick_cm` | decimal, scale 1, required | what the person reads |
| | `diesel_level_liters`, `diesel_fill_percent` | decimal, scale 1 | written by the workflow |
| | `diesel_flag` | choice: `ok`, `check`, `reviewed` | written by the workflow; people set `reviewed` |
| **Purchase** | `diesel_purchased_at` | datetime, required, indexed | |
| | `diesel_bought_liters` | decimal, scale 1, required | |
| | `diesel_total_price` | currency EUR, required | |
| | `diesel_price_per_liter` | decimal, scale 3 | written by the workflow |
| | `diesel_supplier` | text | optional |
| **Heating** | `diesel_heating_at` | datetime, required, indexed | |
| | `diesel_heating` | choice: `on`, `off`, required | |

Readings and purchases keep separate date fields, and every timestamp is a datetime. The tank is often measured on the day it is filled, and the maths needs to know which came first. Saved views: **Readings**, **Purchases**, **Heating** and **Needs a look** (flag `check`). D9 is met by the ordinary list screens.

## Backend: workflows

| Workflow | Starts on | Does |
| --- | --- | --- |
| `diesel-levels` | a reading or purchase created or changed; a manual run for owners | litres, fill % and the consistency flag of readings, the price per litre of purchases (D2, D7, D8) |
| `diesel-measure-reminder` | every morning; every new reading; a manual run for owners | a measuring task when the last reading is 15 days old (D10) |
| `diesel-refill` | `diesel-levels` converting a reading; every new purchase | a refill task when the level falls below the threshold (D11) |
| `diesel-sample-data` | a manual run for owners | files eighteen months of a real tank into an empty list |

**Levels.**
- **The flag.** A reading higher than the one before, plus what was bought between them, plus 5 L, is marked `check`. A reading someone marked `reviewed` stays so.
- **Late purchases.** A purchase checks the reading after it again, so a forgotten purchase filed late clears that reading.
- **Write-back.** The workflow writes back at the item's version, and only what changed. Its own update finds nothing to change. Only a reading whose values changed returns its levels, so the write-back does not ask the refill workflow again.
- **Recalculate all.** The manual run converts every reading again after the tank's settings change (D7).

**Reminder and refill.**
- **The task is the form.** Each asks with a [unique](../workflows.md#tasks) task whose form is the record itself: answering the reminder files the reading, and answering the refill files the purchase. A record filed any other way cancels the open task with a reason.
- **One refill task.** The refill workflow skips a second run on the same reading. A later low reading that finds the task open updates its title and description to the new level, instead of asking twice.
- **Assignees.** A workspace package can name neither people nor groups of the instance it is imported into, so both ship `["role:owners"]`.
- **Notifications.** Assigning a task notifies its assignees in PaperGo; nothing else is sent.

## The dashboard

The app computes everything in the browser from the three types: about 150 items a year.

**Rates.** The forecast uses one rate a day, by season:

| Season | Litres a day | Daily cost at 1.594 €/L |
| --- | --- | --- |
| Heating off (hot water only) | 1.5 | 2.39 € |
| Heating on | 4.5 | 7.17 € |
| Heating on, December to February | 7.5 | 11.96 € |

**The heating.**
- **Recorded.** The heating is as recorded.
- **After the last change.** It is assumed to go on and off on its usual days: the average day of the year of the recorded switches-on and switches-off. The dashboard says when the state is assumed and offers to record it.

**Figures.**

| Figure | Definition |
| --- | --- |
| **Now** (gauge) | The last converted reading, less each day's rate since, plus what was bought after it |
| **Litres and value** | The estimate in litres, and its value at the last price per litre |
| **Days to the reserve** | Days, counted forward at each day's rate, until 25 % is left, and the date |
| **Next order** | The date 200 L are left, and the days to it |
| **Daily cost** | Today's rate × the last price per litre |
| **Level and heating** (chart) | Fill % per reading over time, with the heating seasons shaded red (on) and blue (off), fills marked, and the forecast from the last reading to the next order |
| **Price per litre** (chart) | `total / litres` per purchase |
| **Cost of the diesel used** (bars) | Each interval's consumption (`level before + bought − level after`), spread over its days, valued at the price of the last fill before each day, per month |
| **Consumption** (table) | What each season burnt, from the intervals, beside the rates the forecast uses |

The sample history ends with the tank filled on 3 September 2026. On 4 November 2026 the dashboard shows:
- about 85 % (639 L), worth about 1,018 €;
- the next order on 13 January 2027 and the 25 % reserve a day later;
- 4.2 L a day measured with the heating on, against the 4.5 L used, and 6.5 L in the coldest months, against 7.5 L.

The screen has three forms: **Add a reading**, **Add a purchase** and **The heating went on or off now**.
- Their inputs default to now and work on a phone.
- Decimals are typed in the viewer's number format.
- Live queries show a reading's litres as soon as the workflow writes them.

It also shows the viewer's open measuring and refill tasks, and the readings that need a look, each linked to answer or open it.

## Product notes

- **Measure around a refill.** Two readings around a fill make that interval exact and catch mistakes at once.
- **One tank.** Several tanks would add a **Tank** content type and a lookup field on readings and purchases; the data model allows it without breaking anything.
- **Without the app,** the package alone is useful: the list, the workflows and the views work on their own.
