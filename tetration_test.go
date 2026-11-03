package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestTetrationSettles checks towers against references computed
// independently with Python's decimal module at 400 significant digits:
// finite towers by direct iteration, and towers of height 10^300 by
// iterating x = a**x until the value, or the pair of alternating values,
// stops changing. Tolerances allow for the inexact binary value of bases
// like 2^0.5 or 0.05, which the tower magnifies by a small factor.
func TestTetrationSettles(t *testing.T) {
	tests := []struct {
		expr string
		want string
		tol  int
	}{
		{"(2^0.5) tet 1000", "1.999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999577091985490669388696674865425312093361285695291390400728093071928917432846988120251531638862264497858990425967041945758383897914499629517785548050708627435845400603929496e+0", -1015},
		{"(2^0.5) tet 30", "1.999989394007811653100223109499661581401137302959039354663808915547880391109930257654730151644339560469797937597181238037809693355576798759696730130190967724664815341252019878085735789860241787144824311639692174735725020141725290250077594163311604292066028428505955136107527253677662309928574868853839931703332071664437823840710921e+0", -1015},
		{"0.5 tet (10^300)", "6.411857445049859844862004821148236665628209571911017551396987975434874918787997622340536934991685885923330759169565860161623870766673276317916286045195796288938936692465030336097488268422196437044835977042168675759631730864800940641868662909363958543107574766852600546017914376761661366864847689790094848796557918363690675859122065e-1", -1015},
		{"1.44 tet (10^300)", "2.393811748202946068078598975596791322184853720434387282541962713188686106551005652324119811274273769485696447308082235865763569554768054325964134912462615763021089465864428567059614777014518848617519094195803286655823354592058592474792438333547882431476543091145681083374739671583528236343216788485987638690590657890675747503211899e+0", -1010},
		{"0.05 tet (10^300)", "6.626608389005483449632632860003684925199928895737717702789727206982169338782424285663064626933651063100147733296431848307617470454051040675632590773555225986609697489632932675978332798669438758334261158817556800584600474893054778831464129382590917143201036149693811099088699387182429176391132359483136017262337446774139412681546027e-1", -1010},
		{"0.05 tet (10^300+1)", "1.373593957379562342254031157601289310208996661992783257240682570156977995078746585571696482099612830528904394517781611550687320973865598749098327702676290245438971085415660351996354100469674218404515149836486724154119366702236231397515123019638880903780328958725178332844507519272320937172367796395743803096624809000099203818356497e-1", -1010},
		{"1.1 tet 3", "1.111649800025778100747122923724713335357174035488446562420558232719402827968331772104537455774596755402347699753309908005156118715968666687791005659594548984367475407071653224155657276175594461533086987399423454024075406330000397749372744632131494032999570097286825849972256896915668510284242845450536386921587246103643642651464181e+0", -1020},
		{"0.5 tet 2", "7.071067811865475244008443621048490392848359376884740365883398689953662392310535194251937671638207863675069231154561485124624180279253686063220607485499679157066113329637527963778999752505763910302857350547799858029851372672984310073642587093204445993047761646152421543571607254198813018139976257039948436266982731659044148203103076e-1", -1022},
		{"3.7 tet 3", "8.315251317930008533902165220014649836501954310585056646658906656313151625671352445232938417630032520216973564951187032336471937110829886999210854294121637466627287322758554088514465135144275987019095254337812063845291876817354989506683450756323717284072998729746724661704664861850875933622140422870503730575931624898446793727689811e+71", -1012},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			start := time.Now()
			v, err := evaluate(tt.expr)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if d := time.Since(start); d > costLimit {
				t.Errorf("took %s", d)
			}
			if e := relErr(v, parseRef(t, tt.want)); e.Cmp(pow2(tt.tol)) > 0 {
				t.Errorf("= %s\nwant %s\nrelative error %s, want <= 2^%d", v.Text('g', 40), tt.want[:42], e.Text('g', 3), tt.tol)
			}
		})
	}
}

// TestTetrationExact pins towers with exact answers, including heights far
// beyond anything that could be iterated.
func TestTetrationExact(t *testing.T) {
	huge := "(10^300)"
	tests := []struct {
		expr string
		want string
	}{
		{"1 tet 0", "1"},
		{"1 tet 99999999", "1"},
		{"1 tet " + huge, "1"},
		{"0 tet 0", "1"},
		{"0 tet 1", "0"},
		{"0 tet 2", "1"},
		{"0 tet 3", "0"},
		{"0 tet 4", "1"},
		{"0 tet " + huge, "1"},
		{"0 tet (10^300+1)", "0"},
		{"(0-1) tet 0", "1"},
		{"(0-1) tet 1", "-1"},
		{"(0-1) tet " + huge, "-1"},
		{"(2^0.5) tet " + huge, "2"},
		{"(2^0.5) tet (10^300+1)", "2"},
		{"(0-2) tet 2", "0.25"},
		{"0.5 tet 1", "0.5"},
		{"2 tet 4", "65536"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := evalWithin(t, tt.expr, costLimit)
			if err != nil {
				t.Fatalf("%s: %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("%s = %s, want %s", tt.expr, got, tt.want)
			}
		})
	}
}

// TestTetrationTwoCycleParity checks the two alternating values of a tower
// below e^-e land on the right heights: a tet h and a tet (h+2) agree, a tet h
// and a tet (h+1) do not, for small and astronomically large h alike.
func TestTetrationTwoCycleParity(t *testing.T) {
	val := func(h string) string {
		s, err := evalWithin(t, "0.05 tet ("+h+")", costLimit)
		if err != nil {
			t.Fatalf("0.05 tet %s: %v", h, err)
		}
		return s
	}
	for _, h := range []string{"30000", "10^300", "10^300+7"} {
		a, b, c := val(h), val(h+"+1"), val(h+"+2")
		if a != c {
			t.Errorf("0.05 tet (%s) != 0.05 tet (%s+2): %s vs %s", h, h, a[:20], c[:20])
		}
		if a == b {
			t.Errorf("0.05 tet (%s) == 0.05 tet (%s+1) = %s; want the two cycle values", h, h, a[:20])
		}
	}
}

// TestTetrationLimits checks towers that blow up or never settle fail with the
// right error. A tower that runs to maxTetSteps takes ~0.25s natively but
// several times that under -race, hence the wider time limit here.
func TestTetrationLimits(t *testing.T) {
	huge := "(10^300)"
	tests := []struct {
		expr    string
		wantErr []error // any of these
	}{
		{"2 tet 6", []error{errResultTooLarge}},
		{"2 tet " + huge, []error{errResultTooLarge}},
		{"1.5 tet " + huge, []error{errResultTooLarge}},
		{"10 tet 3", []error{errResultTooLarge}},
		// Just below e^(1/e) ~ 1.444667861: converges, but far too slowly.
		{"1.4446 tet " + huge, []error{errTetSteps}},
		// Just above it: diverges, but only after a very long plateau.
		{"1.4448 tet " + huge, []error{errTetSteps, errResultTooLarge}},
		// Just above e^-e ~ 0.065988: converges far too slowly.
		{"0.0661 tet " + huge, []error{errTetSteps}},
		{"1.444 tet " + huge, []error{errTetSteps}},
		// The cap bounds explicit heights too, not just huge ones.
		{"1.4446 tet 20000", []error{errTetSteps}},
		{"(0-2) tet 3", []error{errNotReal}},
		{"2 tet 2.5", []error{errTetHeight}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := evalWithin(t, tt.expr, 5*costLimit)
			for _, w := range tt.wantErr {
				if errors.Is(err, w) {
					return
				}
			}
			t.Errorf("%s: err = %v, want one of %v", tt.expr, err, tt.wantErr)
		})
	}
	if !strings.Contains(errTetSteps.Error(), "10000") {
		t.Errorf("errTetSteps message %q does not name the step cap", errTetSteps)
	}
}
