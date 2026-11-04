package content

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/stephenafamo/scan"
)

// ExactScale is the fraction digits of exact number keys.
const ExactScale = 28

// Measure is what the database works out of one field's values in each
// group of an aggregation. It always counts them.
type Measure struct {
	Field         Field
	Sum, Min, Max bool
}

// Aggregation asks for figures of a plan's rows, in the groups of its Groups
// or, without any, as one group that exists even when no row matches.
type Aggregation struct {
	Groups   []GroupBy
	Measures []Measure
}

// Figures is what a measure worked out in one group. Sum is the exact sum of
// a field's stored values: integer units, or ExactScale decimal digits for
// exact numbers. Min and Max are raw column values. All are nil without
// values or when not asked for.
type Figures struct {
	Count    int
	Sum      *big.Int
	Min, Max any
}

// AggregateRow is one group of an aggregation: the raw value of each group
// column, its row count and the figures of each measure, in the order the
// aggregation names them. Its slices are reused between rows.
type AggregateRow struct {
	Keys    []any
	Count   int
	Figures []Figures
}

// A sum adds up limbs of each value: every engine stays within 64-bit
// integers and neither loses digits nor overflows, which sum() alone
// guarantees on neither. Each limb sum fits as long as fewer than 9e9 rows
// are read.
var (
	// An integer splits at 1e9; truncating division keeps both parts' sign.
	integerWeights = limbWeights(9, 0)
	// An exact key is a sign digit, 1 or 0, and 57 digits, complemented when
	// negative, as field_values checks: a limb of 3 digits and six of 9.
	exactWeights = limbWeights(54, 45, 36, 27, 18, 9, 0)
)

func limbWeights(exponents ...int64) []*big.Int {
	out := make([]*big.Int, len(exponents))
	for i, e := range exponents {
		out[i] = new(big.Int).Exp(big.NewInt(10), big.NewInt(e), nil)
	}
	return out
}

func sumWeights(kind Kind) []*big.Int {
	if kind == KindExact {
		return exactWeights
	}
	return integerWeights
}

// sumColumns is the SQL of a measure's limb sums.
func sumColumns(alias string, kind Kind) []string {
	total := func(value string) string { return "CAST(sum(" + value + ") AS BIGINT)" }
	if kind != KindExact {
		return []string{total(alias + "/1000000000"), total(alias + "%1000000000")}
	}
	columns := make([]string, 0, len(exactWeights))
	start, size := 2, 3
	for range exactWeights {
		digits := "CAST(substr(" + alias + "," + strconv.Itoa(start) + "," + strconv.Itoa(size) + ") AS BIGINT)"
		columns = append(columns, total("CASE WHEN substr("+alias+",1,1)='0' THEN "+digits+"-"+strings.Repeat("9", size)+" ELSE "+digits+" END"))
		start, size = start+size, 9
	}
	return columns
}

// sumOf puts limb sums back together; nil limbs mean no values.
func sumOf(kind Kind, limbs []any) (*big.Int, error) {
	if limbs[0] == nil {
		return nil, nil
	}
	sum := new(big.Int)
	limb := new(big.Int)
	for i, weight := range sumWeights(kind) {
		n, ok := limbs[i].(int64)
		if !ok {
			return nil, fmt.Errorf("unexpected sum column %T", limbs[i])
		}
		sum.Add(sum, limb.Mul(limb.SetInt64(n), weight))
	}
	return sum, nil
}

func groupAlias(i int) string { return "group_" + strconv.Itoa(i) }

func measureAlias(i int) string { return "measure_" + strconv.Itoa(i) }

// count reads a counting column.
func count(v any) (int, error) {
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected count column %T", v)
	}
	return int(n), nil
}

// Aggregate calls fn with each group of a query compiled from a plan with an
// Aggregation, in no order: the database works the figures out and returns
// one row a group. It reads at most limit rows, so a caller that passes one
// more than it accepts sees that in the groups' counts.
func (r *Repo) Aggregate(ctx context.Context, q *Query, limit int, fn func(AggregateRow) error) error {
	a := q.aggregation
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return err
	}
	columns := make([]string, 0, len(a.Groups)+1+len(a.Measures)*2)
	aliases := make([]string, len(a.Groups))
	for i := range a.Groups {
		aliases[i] = groupAlias(i)
	}
	columns = append(append(columns, aliases...), "count(*)")
	for i, m := range a.Measures {
		alias := measureAlias(i)
		columns = append(columns, "count("+alias+")")
		if m.Sum {
			columns = append(columns, sumColumns(alias, q.measureKinds[i])...)
		}
		if m.Min {
			columns = append(columns, "min("+alias+")")
		}
		if m.Max {
			columns = append(columns, "max("+alias+")")
		}
	}
	statement := "WITH eligible AS (SELECT * FROM (" + query + ") AS matched LIMIT ?) SELECT " + strings.Join(columns, ",") + " FROM eligible"
	if len(aliases) > 0 {
		statement += " GROUP BY " + strings.Join(aliases, ",")
	}
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	row := AggregateRow{Keys: values[:len(a.Groups)], Figures: make([]Figures, len(a.Measures))}
	return r.each(ctx, func(rows scan.Rows) error {
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		at := len(a.Groups)
		var err error
		if row.Count, err = count(values[at]); err != nil {
			return err
		}
		at++
		for i, m := range a.Measures {
			f := Figures{}
			if f.Count, err = count(values[at]); err != nil {
				return err
			}
			at++
			if m.Sum {
				limbs := len(sumWeights(q.measureKinds[i]))
				if f.Sum, err = sumOf(q.measureKinds[i], values[at:at+limbs]); err != nil {
					return err
				}
				at += limbs
			}
			if m.Min {
				f.Min = values[at]
				at++
			}
			if m.Max {
				f.Max = values[at]
				at++
			}
			row.Figures[i] = f
		}
		return fn(row)
	}, statement, append(args, limit)...)
}
