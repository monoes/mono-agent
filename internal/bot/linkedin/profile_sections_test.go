//go:build !nosocial

package linkedin

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

func TestDates(t *testing.T) {
	cases := []struct{ in, rng, start, end, dur string }{
		{"Jun 2025 - Present · 1 yr 4 mos", "Jun 2025 - Present", "Jun 2025", "Present", "1 yr 4 mos"},
		{"Sep 1988 – Jun 1992", "Sep 1988 - Jun 1992", "Sep 1988", "Jun 1992", ""},
		{"2007 – Oct 2024", "2007 - Oct 2024", "2007", "Oct 2024", ""},
		{"2011 – Present", "2011 - Present", "2011", "Present", ""},
		{"2019", "2019", "2019", "", ""},
	}
	for _, c := range cases {
		rng, start, end, dur, ok := dates(c.in)
		if !ok || rng != c.rng || start != c.start || end != c.end || dur != c.dur {
			t.Errorf("dates(%q) = %q %q %q %q %v", c.in, rng, start, end, dur, ok)
		}
	}
	for _, not := range []string{"Berlin, Germany", "Co-Founder&CTO", "Full-time · 9 yrs 2 mos", "Class of the 2020 cohort"} {
		if isDateLine(not) {
			t.Errorf("%q read as a date line", not)
		}
	}
}

// The line shapes a real capture of the server-driven details pages gave
// (2026-09), as profileSectionJS reports them.
func TestParseExperience(t *testing.T) {
	got := parseExperience([]rawEntry{
		{Lines: []string{"Co-Founder", "Teloriva · Self-employed", "Jun 2025 - Present · 1 yr 4 mos", "Berlin, Germany · Remote"},
			URL: "https://www.linkedin.com/company/1/"},
		{Lines: []string{"Founder", "Example Co", "Apr 2026 - Present · 6 mos", "Berlin, Germany · On-site", "Knowledge Engineering, Artificial Intelligence (AI) and +3 skills"}},
		{Lines: []string{"Next Play Example", "18 yrs 1 mo", "San Francisco Bay Area"}, URL: "https://www.linkedin.com/company/2/",
			Roles: []rawRole{
				{Lines: []string{"Founding Partner", "Jul 2020 - Present · 6 yrs 3 mos"}, Description: "Coach and invest."},
				{Lines: []string{"Investor/Advisor", "Sep 2008 - Jun 2020 · 11 yrs 10 mos"}},
			}},
		{Lines: []string{"Researcher", "Example Deere", "Aug 2013 - May 2014 · 10 mos", "kaiserslautern",
			"Research about improving software development for large distributed teams by mixing agile methods with six sigma tools."}},
		{Lines: []string{"Co-Founder - CTO", "flat-example.com", "Jan 2016 - Apr 2019 · 3 yrs 4 mos", "Remote"}},
	})
	want := []Position{
		{Title: "Co-Founder", Company: "Teloriva", CompanyURL: "https://www.linkedin.com/company/1/", EmploymentType: "Self-employed",
			DateRange: "Jun 2025 - Present", Start: "Jun 2025", End: "Present", Duration: "1 yr 4 mos", Location: "Berlin, Germany", LocationType: "Remote"},
		{Title: "Founder", Company: "Example Co", DateRange: "Apr 2026 - Present", Start: "Apr 2026", End: "Present", Duration: "6 mos",
			Location: "Berlin, Germany", LocationType: "On-site", Skills: "Knowledge Engineering, Artificial Intelligence (AI) and +3 skills"},
		{Title: "Founding Partner", Company: "Next Play Example", CompanyURL: "https://www.linkedin.com/company/2/", DateRange: "Jul 2020 - Present",
			Start: "Jul 2020", End: "Present", Duration: "6 yrs 3 mos", Location: "San Francisco Bay Area", Description: "Coach and invest."},
		{Title: "Investor/Advisor", Company: "Next Play Example", CompanyURL: "https://www.linkedin.com/company/2/", DateRange: "Sep 2008 - Jun 2020",
			Start: "Sep 2008", End: "Jun 2020", Duration: "11 yrs 10 mos", Location: "San Francisco Bay Area"},
		{Title: "Researcher", Company: "Example Deere", DateRange: "Aug 2013 - May 2014", Start: "Aug 2013", End: "May 2014", Duration: "10 mos",
			Location: "kaiserslautern", Description: "Research about improving software development for large distributed teams by mixing agile methods with six sigma tools."},
		{Title: "Co-Founder - CTO", Company: "flat-example.com", DateRange: "Jan 2016 - Apr 2019", Start: "Jan 2016", End: "Apr 2019", Duration: "3 yrs 4 mos", LocationType: "Remote"},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("got %s", g)
	}
	if cur, ok := currentPosition(got); !ok || cur.Title != "Co-Founder" {
		t.Fatalf("current = %+v %v", cur, ok)
	}
	if _, ok := currentPosition(got[4:]); ok {
		t.Fatal("a past position read as current")
	}
}

func TestParseEducation(t *testing.T) {
	got := parseEducation([]rawEntry{
		{Lines: []string{"Example School of Business", "Bachelor of Science, Economics", "Sep 1988 – Jun 1992"}, URL: "https://www.linkedin.com/school/5/"},
		{Lines: []string{"Example University", "European Master of Software Engineering", "2012 – 2013", "Grade: A", "Activities and societies: Chess"},
			Description: "- Thesis\n- TA"},
		{Lines: []string{"Example College"}},
	})
	want := []School{
		{School: "Example School of Business", SchoolURL: "https://www.linkedin.com/school/5/", Degree: "Bachelor of Science", FieldOfStudy: "Economics",
			DateRange: "Sep 1988 - Jun 1992", Start: "Sep 1988", End: "Jun 1992"},
		{School: "Example University", Degree: "European Master of Software Engineering", DateRange: "2012 - 2013", Start: "2012", End: "2013",
			Grade: "A", Activities: "Chess", Description: "- Thesis\n- TA"},
		{School: "Example College"},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("got %s", g)
	}
}

func TestGetProfileDetails(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("server-driven details pages", func(t *testing.T) {
		p, rec := newPage(t, b,
			bottest.Route{Pattern: "https://www.linkedin.com/in/*/details/experience/", File: "testdata/profile_details_experience_sdui.html"},
			bottest.Route{Pattern: "https://www.linkedin.com/in/*/details/education/", File: "testdata/profile_details_education_sdui.html"},
			bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/profile_sdui_other.html"})
		res, err := call(t, &LinkedInBot{}, p, "get_profile_data", "https://www.linkedin.com/in/pat-example-test/")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		exp := fromJSONList[Position](m["experience"].([]interface{}))
		wantExp := []Position{
			{Title: "Chief Technology Officer", Company: "Example Labs", CompanyURL: "https://www.linkedin.com/company/1111/", EmploymentType: "Full-time",
				DateRange: "Mar 2021 - Present", Start: "Mar 2021", End: "Present", Duration: "5 yrs 7 mos", Location: "Lisbon, Portugal",
				Description: "Leads the platform team.\nHired the first twenty engineers."},
			{Title: "Software Engineer", Company: "Example Labs", CompanyURL: "https://www.linkedin.com/company/1111/", EmploymentType: "Part-time",
				DateRange: "Aug 2017 - Feb 2021", Start: "Aug 2017", End: "Feb 2021", Duration: "3 yrs 7 mos", Location: "Porto, Portugal", LocationType: "Hybrid",
				Description: "Built the billing service."},
			{Title: "Product Designer", Company: "Sample Studio", CompanyURL: "https://www.linkedin.com/company/2222/", EmploymentType: "Contract",
				DateRange: "Jan 2015 - Jul 2017", Start: "Jan 2015", End: "Jul 2017", Duration: "2 yrs 7 mos", Location: "Berlin, Germany", LocationType: "Remote",
				Skills: "Figma, Design Systems and +2 skills"},
			{Title: "Barista", Company: "Corner Cafe", DateRange: "2012 - 2014", Start: "2012", End: "2014"},
		}
		if !reflect.DeepEqual(exp, wantExp) {
			g, _ := json.MarshalIndent(exp, "", " ")
			t.Errorf("experience = %s", g)
		}
		edu := fromJSONList[School](m["education"].([]interface{}))
		wantEdu := []School{{School: "Example University", SchoolURL: "https://www.linkedin.com/school/3333/", Degree: "Master of Science - MS",
			FieldOfStudy: "Computer Science, Robotics", DateRange: "Sep 2010 - Jun 2012", Start: "Sep 2010", End: "Jun 2012", Grade: "1st class",
			Activities: "Robotics club, Chess", Description: "- Thesis on swarm navigation\n- Teaching assistant"}}
		if !reflect.DeepEqual(edu, wantEdu) {
			g, _ := json.MarshalIndent(edu, "", " ")
			t.Errorf("education = %s", g)
		}
		// The top card is still read, and the current position is the job title.
		if m["full_name"] != "Pat Example" || m["headline"] != "Co-chair, Example Foundation" ||
			m["job_title"] != "Chief Technology Officer" || m["current_company"] != "Example Labs" ||
			m["cover_image_url"] != "https://media.licdn.com/dms/image/v2/fake/profile-displaybackgroundimage-shrink_350_1400/banner.jpg" {
			t.Errorf("profile = %v", m)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/in/pat-example-test/details/experience/")) != 1 ||
			len(rec.Matching("GET", "https://www.linkedin.com/in/pat-example-test/details/education/")) != 1 {
			t.Errorf("requests = %v", rec.Requests())
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("a read made writes: %v", w)
		}
	})

	t.Run("classic details pages", func(t *testing.T) {
		p, _ := newPage(t, b,
			bottest.Route{Pattern: "https://www.linkedin.com/in/*/details/experience/", File: "testdata/profile_details_experience_classic.html"},
			bottest.Route{Pattern: "https://www.linkedin.com/in/*/details/education/", File: "testdata/profile_details_education_classic.html"},
			profileRoute)
		res, err := call(t, &LinkedInBot{}, p, "get_profile_data", "https://www.linkedin.com/in/ada-first-test/")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		exp := fromJSONList[Position](m["experience"].([]interface{}))
		wantExp := []Position{
			{Title: "Robotics Engineer", Company: "Example Works", CompanyURL: "https://www.linkedin.com/company/4444/", EmploymentType: "Full-time",
				DateRange: "Jan 2022 - Present", Start: "Jan 2022", End: "Present", Duration: "4 yrs 9 mos", Location: "Lisbon, Portugal",
				Description: "Builds walking robots."},
			{Title: "Intern", Company: "Example Works", CompanyURL: "https://www.linkedin.com/company/4444/", EmploymentType: "Internship",
				DateRange: "Oct 2020 - Dec 2021", Start: "Oct 2020", End: "Dec 2021", Duration: "1 yr 3 mos", Location: "Porto, Portugal", LocationType: "On-site"},
			{Title: "Data Analyst", Company: "Sample Bank", CompanyURL: "https://www.linkedin.com/company/5555/", EmploymentType: "Full-time",
				DateRange: "Jun 2016 - Sep 2020", Start: "Jun 2016", End: "Sep 2020", Duration: "4 yrs 4 mos", Location: "Madrid, Spain",
				Description: "Risk dashboards for the retail team.", Skills: "SQL · Python"},
		}
		if !reflect.DeepEqual(exp, wantExp) {
			g, _ := json.MarshalIndent(exp, "", " ")
			t.Errorf("experience = %s", g)
		}
		edu := fromJSONList[School](m["education"].([]interface{}))
		wantEdu := []School{
			{School: "Sample Institute of Technology", SchoolURL: "https://www.linkedin.com/school/6666/", Degree: "Bachelor of Engineering - BE",
				FieldOfStudy: "Mechanical Engineering", DateRange: "2012 - 2016", Start: "2012", End: "2016", Grade: "3.8", Description: "Formula Student team lead."},
			{School: "Example High School", SchoolURL: "https://www.linkedin.com/school/7777/", Degree: "High School Diploma", DateRange: "2012", Start: "2012"},
		}
		if !reflect.DeepEqual(edu, wantEdu) {
			g, _ := json.MarshalIndent(edu, "", " ")
			t.Errorf("education = %s", g)
		}
		if m["job_title"] != "Robotics Engineer" || m["current_company"] != "Example Works" || m["headline"] != "Robotics engineer at Example Works" {
			t.Errorf("job_title = %v, current_company = %v", m["job_title"], m["current_company"])
		}
	})

	// includeDetails=false reads the profile page only.
	t.Run("without details", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		res, err := call(t, &LinkedInBot{}, p, "get_profile_data", "https://www.linkedin.com/in/ada-first-test/", "false")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		if len(rec.Matching("GET", "https://www.linkedin.com/in/*/details/*")) != 0 {
			t.Fatalf("opened a details page: %v", rec.Requests())
		}
		if m["job_title"] != m["headline"] {
			t.Errorf("job_title = %v, want the headline when no position is known", m["job_title"])
		}
	})
}
