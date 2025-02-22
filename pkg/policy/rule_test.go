// SPDX-License-Identifier: Apache-2.0
// Copyright 2016-2021 Authors of Cilium

// +build !privileged_tests

package policy

import (
	"bytes"
	stdlog "log"
	"net"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/checker"
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/cilium/pkg/policy/api/kafka"

	. "gopkg.in/check.v1"
)

func (ds *PolicyTestSuite) TestL4Policy(c *C) {
	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	fromBar := &SearchContext{From: labels.ParseSelectLabelArray("bar")}
	toFoo := &SearchContext{To: labels.ParseSelectLabelArray("foo")}
	fromFoo := &SearchContext{From: labels.ParseSelectLabelArray("foo")}

	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
							{Port: "8080", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
			Egress: []api.EgressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "3000", Protocol: api.ProtoAny},
						},
					}},
				},
			},
		},
	}

	l7rules := api.L7Rules{
		HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
	}
	l7map := L7DataMap{
		wildcardCachedSelector: &PerSelectorPolicy{
			L7Rules: l7rules,
		},
	}

	expected := NewL4Policy(0)
	expected.Ingress["80/TCP"] = &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "http", L7RulesPerSelector: l7map, Ingress: true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}
	expected.Ingress["8080/TCP"] = &L4Filter{
		Port: 8080, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "http", L7RulesPerSelector: l7map, Ingress: true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	expected.Egress["3000/TCP"] = &L4Filter{
		Port: 3000, Protocol: api.ProtoTCP, U8Proto: 6, Ingress: false,
		wildcard: wildcardCachedSelector,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: nil,
		},
		DerivedFromRules: labels.LabelArrayList{nil},
	}
	expected.Egress["3000/UDP"] = &L4Filter{
		Port: 3000, Protocol: api.ProtoUDP, U8Proto: 17, Ingress: false,
		wildcard: wildcardCachedSelector,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: nil,
		},
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	ingressState := traceState{}
	egressState := traceState{}
	res := NewL4Policy(0)
	var err error
	res.Ingress, err =
		rule1.resolveIngressPolicy(testPolicyContext, toBar, &ingressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res.Ingress, Not(IsNil))

	res.Egress, err =
		rule1.resolveEgressPolicy(testPolicyContext, fromBar, &egressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res.Egress, Not(IsNil))

	c.Assert(res, checker.Equals, expected)
	c.Assert(ingressState.selectedRules, Equals, 1)
	c.Assert(ingressState.matchedRules, Equals, 1)

	c.Assert(egressState.selectedRules, Equals, 1)
	c.Assert(egressState.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	// Foo isn't selected in the rule1's policy.
	ingressState = traceState{}
	egressState = traceState{}

	res1, err := rule1.resolveIngressPolicy(testPolicyContext, toFoo, &ingressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	res2, err := rule1.resolveEgressPolicy(testPolicyContext, fromFoo, &ingressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)

	c.Assert(res1, IsNil)
	c.Assert(res2, IsNil)
	c.Assert(ingressState.selectedRules, Equals, 0)
	c.Assert(ingressState.matchedRules, Equals, 0)
	c.Assert(egressState.selectedRules, Equals, 0)
	c.Assert(egressState.matchedRules, Equals, 0)

	// This rule actually overlaps with the existing ingress "http" rule,
	// so we'd expect it to merge.
	rule2 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					// Note that this allows all on 80, so the result should wildcard HTTP
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
			Egress: []api.EgressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "3000", Protocol: api.ProtoAny},
						},
					}},
				},
			},
		},
	}

	expected = NewL4Policy(0)
	expected.Ingress["80/TCP"] = &L4Filter{
		Port:     80,
		Protocol: api.ProtoTCP,
		U8Proto:  6,
		wildcard: wildcardCachedSelector,
		L7Parser: ParserTypeHTTP,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}, {}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}
	expected.Egress["3000/TCP"] = &L4Filter{
		Port: 3000, Protocol: api.ProtoTCP, U8Proto: 6, Ingress: false,
		wildcard: wildcardCachedSelector,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: nil,
		},
		DerivedFromRules: labels.LabelArrayList{nil},
	}
	expected.Egress["3000/UDP"] = &L4Filter{
		Port: 3000, Protocol: api.ProtoUDP, U8Proto: 17, Ingress: false,
		wildcard: wildcardCachedSelector,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: nil,
		},
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	ingressState = traceState{}
	egressState = traceState{}
	res = NewL4Policy(0)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{To: labels.ParseSelectLabelArray("bar"), Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	res.Ingress, err = rule2.resolveIngressPolicy(testPolicyContext, &ctx, &ingressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res.Ingress, Not(IsNil))

	c.Log(buffer)

	res.Egress, err = rule2.resolveEgressPolicy(testPolicyContext, fromBar, &egressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res.Egress, Not(IsNil))

	c.Assert(len(res.Ingress), Equals, 1)
	c.Assert(res, checker.Equals, expected)
	c.Assert(ingressState.selectedRules, Equals, 1)
	c.Assert(ingressState.matchedRules, Equals, 1)

	c.Assert(egressState.selectedRules, Equals, 1)
	c.Assert(egressState.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	ingressState = traceState{}
	egressState = traceState{}

	res1, err = rule2.resolveIngressPolicy(testPolicyContext, toFoo, &ingressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res1, IsNil)

	res2, err = rule2.resolveEgressPolicy(testPolicyContext, fromFoo, &egressState, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res2, IsNil)

	c.Assert(ingressState.selectedRules, Equals, 0)
	c.Assert(ingressState.matchedRules, Equals, 0)

	c.Assert(egressState.selectedRules, Equals, 0)
	c.Assert(egressState.matchedRules, Equals, 0)
}

func (ds *PolicyTestSuite) TestMergeL4PolicyIngress(c *C) {
	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	//toFoo := &SearchContext{To: labels.ParseSelectLabelArray("foo")}

	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{fooSelector},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{bazSelector},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	}

	mergedES := L7DataMap{
		cachedFooSelector: nil,
		cachedBazSelector: nil,
	}
	expected := L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		L7Parser: ParserTypeNone, L7RulesPerSelector: mergedES, Ingress: true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state := traceState{}
	res, err := rule1.resolveIngressPolicy(testPolicyContext, toBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)
}

func (ds *PolicyTestSuite) TestMergeL4PolicyEgress(c *C) {

	buffer := new(bytes.Buffer)
	fromBar := &SearchContext{
		From:    labels.ParseSelectLabelArray("bar"),
		Logging: stdlog.New(buffer, "", 0),
		Trace:   TRACE_VERBOSE,
	}

	// bar can access foo with TCP on port 80, and baz with TCP on port 80.
	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: []api.EndpointSelector{fooSelector},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: []api.EndpointSelector{bazSelector},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	}

	mergedES := L7DataMap{
		cachedFooSelector: nil,
		cachedBazSelector: nil,
	}
	expected := L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		L7Parser: ParserTypeNone, L7RulesPerSelector: mergedES, Ingress: false,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state := traceState{}
	res, err := rule1.resolveEgressPolicy(testPolicyContext, fromBar, &state, L4PolicyMap{}, nil, nil)

	c.Log(buffer)

	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)
}

func (ds *PolicyTestSuite) TestMergeL7PolicyIngress(c *C) {
	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	toFoo := &SearchContext{To: labels.ParseSelectLabelArray("foo")}

	fooSelectorSlice := []api.EndpointSelector{
		fooSelector,
	}
	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					// Note that this allows all on 80, so the result should wildcard HTTP
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: fooSelectorSlice,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		},
	}

	expected := L4PolicyMap{"80/TCP": &L4Filter{
		Port:     80,
		Protocol: api.ProtoTCP,
		U8Proto:  6,
		wildcard: wildcardCachedSelector,
		L7Parser: ParserTypeHTTP,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}, {}},
				},
			},
			cachedFooSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state := traceState{}
	res, err := rule1.resolveIngressPolicy(testPolicyContext, toBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	state = traceState{}
	res, err = rule1.resolveIngressPolicy(testPolicyContext, toFoo, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	rule2 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: fooSelectorSlice,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
			},
		},
	}

	l7rules := api.L7Rules{
		Kafka: []kafka.PortRule{{Topic: "foo"}},
	}
	l7map := L7DataMap{
		wildcardCachedSelector: &PerSelectorPolicy{
			L7Rules: l7rules,
		},
		cachedFooSelector: &PerSelectorPolicy{
			L7Rules: l7rules,
		},
	}

	expected = L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "kafka", L7RulesPerSelector: l7map, Ingress: true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state = traceState{}
	res, err = rule2.resolveIngressPolicy(testPolicyContext, toBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	state = traceState{}
	res, err = rule2.resolveIngressPolicy(testPolicyContext, toFoo, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Resolve rule1's policy, then try to add rule2.
	res, err = rule1.resolveIngressPolicy(testPolicyContext, toBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))

	state = traceState{}
	_, err = rule2.resolveIngressPolicy(testPolicyContext, toBar, &state, res, nil, nil)

	c.Assert(err, Not(IsNil))
	res.Detach(testSelectorCache)

	// Similar to 'rule2', but with different topics for the l3-dependent
	// rule and the l4-only rule.
	rule3 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: fooSelectorSlice,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "bar"},
							},
						},
					}},
				},
			},
		},
	}

	fooRules := api.L7Rules{
		Kafka: []kafka.PortRule{{Topic: "foo"}},
	}

	barRules := api.L7Rules{
		Kafka: []kafka.PortRule{{Topic: "bar"}},
	}

	// The L3-dependent L7 rules are not merged together.
	l7map = L7DataMap{
		cachedFooSelector: &PerSelectorPolicy{
			L7Rules: fooRules,
		},
		wildcardCachedSelector: &PerSelectorPolicy{
			L7Rules: barRules,
		},
	}
	expected = L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "kafka", L7RulesPerSelector: l7map, Ingress: true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state = traceState{}
	res, err = rule3.resolveIngressPolicy(testPolicyContext, toBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)
}

func (ds *PolicyTestSuite) TestMergeL7PolicyEgress(c *C) {
	fromBar := &SearchContext{From: labels.ParseSelectLabelArray("bar")}
	fromFoo := &SearchContext{From: labels.ParseSelectLabelArray("foo")}

	fooSelector := []api.EndpointSelector{
		api.NewESFromLabels(labels.ParseSelectLabel("foo")),
	}

	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Egress: []api.EgressRule{
				{
					// Note that this allows all on 80, so the result should wildcard HTTP
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/public"},
							},
						},
					}},
				},
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: fooSelector,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/private"},
							},
						},
					}},
				},
			},
		},
	}

	expected := L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: ParserTypeHTTP,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/public", Method: "GET"}, {}},
				},
			},
			cachedFooSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/private", Method: "GET"}},
				},
			},
		},
		Ingress:          false,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state := traceState{}
	res, err := rule1.resolveEgressPolicy(testPolicyContext, fromBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	state = traceState{}
	res, err = rule1.resolveEgressPolicy(testPolicyContext, fromFoo, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	rule2 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Egress: []api.EgressRule{
				{
					// Note that this allows all on 9092, so the result should wildcard Kafka
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "9092", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "9092", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: fooSelector,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "9092", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
			},
		},
	}

	expected = L4PolicyMap{"9092/TCP": &L4Filter{
		Port: 9092, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: ParserTypeKafka,
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					Kafka: []kafka.PortRule{{Topic: "foo"}, {}},
				},
			},
			cachedFooSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					Kafka: []kafka.PortRule{{Topic: "foo"}},
				},
			},
		},
		Ingress:          false,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state = traceState{}
	res, err = rule2.resolveEgressPolicy(testPolicyContext, fromBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)

	state = traceState{}
	res, err = rule2.resolveEgressPolicy(testPolicyContext, fromFoo, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Resolve rule1's policy, then try to add rule2.
	res, err = rule1.resolveEgressPolicy(testPolicyContext, fromBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	res.Detach(testSelectorCache)

	// Similar to 'rule2', but with different topics for the l3-dependent
	// rule and the l4-only rule.
	rule3 := &rule{
		Rule: api.Rule{
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: fooSelector,
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []kafka.PortRule{
								{Topic: "bar"},
							},
						},
					}},
				},
			},
		},
	}

	fooRules := api.L7Rules{
		Kafka: []kafka.PortRule{{Topic: "foo"}},
	}
	barRules := api.L7Rules{
		Kafka: []kafka.PortRule{{Topic: "bar"}},
	}

	// The l3-dependent l7 rules are not merged together.
	l7map := L7DataMap{
		cachedFooSelector: &PerSelectorPolicy{
			L7Rules: fooRules,
		},
		wildcardCachedSelector: &PerSelectorPolicy{
			L7Rules: barRules,
		},
	}
	expected = L4PolicyMap{"80/TCP": &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "kafka", L7RulesPerSelector: l7map, Ingress: false,
		DerivedFromRules: labels.LabelArrayList{nil},
	}}

	state = traceState{}
	res, err = rule3.resolveEgressPolicy(testPolicyContext, fromBar, &state, L4PolicyMap{}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(res, checker.Equals, expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 1)
	res.Detach(testSelectorCache)
	expected.Detach(testSelectorCache)
}

func (ds *PolicyTestSuite) TestRuleWithNoEndpointSelector(c *C) {
	apiRule1 := api.Rule{
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromCIDR: []api.CIDR{
						"10.0.1.0/24",
						"192.168.2.0",
						"10.0.3.1",
						"2001:db8::1/48",
						"2001:db9::",
					},
				},
			},
		},
		Egress: []api.EgressRule{
			{
				EgressCommonRule: api.EgressCommonRule{
					ToCIDR: []api.CIDR{
						"10.1.0.0/16",
						"2001:dbf::/64",
					},
				},
			}, {
				EgressCommonRule: api.EgressCommonRule{
					ToCIDRSet: []api.CIDRRule{{Cidr: api.CIDR("10.0.0.0/8"), ExceptCIDRs: []api.CIDR{"10.96.0.0/12"}}},
				},
			},
		},
	}

	err := apiRule1.Sanitize()
	c.Assert(err, Not(IsNil))
}

func (ds *PolicyTestSuite) TestL3Policy(c *C) {
	apiRule1 := api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),

		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromCIDR: []api.CIDR{
						"10.0.1.0/24",
						"192.168.2.0",
						"10.0.3.1",
						"2001:db8::1/48",
						"2001:db9::",
					},
				},
			},
		},
		Egress: []api.EgressRule{
			{
				EgressCommonRule: api.EgressCommonRule{
					ToCIDR: []api.CIDR{
						"10.1.0.0/16",
						"2001:dbf::/64",
					},
				},
			}, {
				EgressCommonRule: api.EgressCommonRule{
					ToCIDRSet: []api.CIDRRule{{Cidr: api.CIDR("10.0.0.0/8"), ExceptCIDRs: []api.CIDR{"10.96.0.0/12"}}},
				},
			},
		},
	}

	err := apiRule1.Sanitize()
	c.Assert(err, IsNil)

	rule1 := &rule{Rule: apiRule1}
	err = rule1.Sanitize()
	c.Assert(err, IsNil)

	expected := NewCIDRPolicy()
	expected.Ingress.Map["10.0.1.0/24"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 0, 1, 0}, Mask: []byte{255, 255, 255, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Ingress.Map["192.168.2.0/24"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{192, 168, 2, 0}, Mask: []byte{255, 255, 255, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Ingress.Map["10.0.3.1/32"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 0, 3, 1}, Mask: []byte{255, 255, 255, 255}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Ingress.IPv4PrefixCount[32] = 1
	expected.Ingress.IPv4PrefixCount[24] = 2
	expected.Ingress.Map["2001:db8::/48"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{0x20, 1, 0xd, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Mask: []byte{255, 255, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Ingress.Map["2001:db9::/128"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{0x20, 1, 0xd, 0xb9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Mask: []byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Ingress.IPv6PrefixCount[128] = 1
	expected.Ingress.IPv6PrefixCount[48] = 1
	expected.Egress.Map["10.1.0.0/16"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 1, 0, 0}, Mask: []byte{255, 255, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.Map["10.128.0.0/9"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 128, 0, 0}, Mask: []byte{255, 128, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.Map["10.0.0.0/10"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 0, 0, 0}, Mask: []byte{255, 192, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.Map["10.64.0.0/11"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 64, 0, 0}, Mask: []byte{255, 224, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.Map["10.112.0.0/12"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{10, 112, 0, 0}, Mask: []byte{255, 240, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.IPv4PrefixCount[16] = 1
	expected.Egress.IPv4PrefixCount[12] = 1
	expected.Egress.IPv4PrefixCount[11] = 1
	expected.Egress.IPv4PrefixCount[10] = 1
	expected.Egress.IPv4PrefixCount[9] = 1
	expected.Egress.Map["2001:dbf::/64"] = &CIDRPolicyMapRule{Prefix: net.IPNet{IP: []byte{0x20, 1, 0xd, 0xbf, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Mask: []byte{255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0}}, DerivedFromRules: labels.LabelArrayList{nil}}
	expected.Egress.IPv6PrefixCount[64] = 1

	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	state := traceState{}
	res := rule1.resolveCIDRPolicy(toBar, &state, NewCIDRPolicy())
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	// Must be parsable, make sure Validate fails when not.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDR: []api.CIDR{"10.0.1..0/24"},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))

	// Test CIDRRule with no provided CIDR or ExceptionCIDR.
	// Should fail as CIDR is required.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDRSet: []api.CIDRRule{{Cidr: "", ExceptCIDRs: nil}},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))

	// Test CIDRRule with only CIDR provided; should not fail, as ExceptionCIDR
	// is optional.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDRSet: []api.CIDRRule{{Cidr: "10.0.1.0/24", ExceptCIDRs: nil}},
			},
		}},
	}.Sanitize()
	c.Assert(err, IsNil)

	// Cannot provide just an IP to a CIDRRule; Cidr must be of format
	// <IP>/<prefix>.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDRSet: []api.CIDRRule{{Cidr: "10.0.1.32", ExceptCIDRs: nil}},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))

	// Cannot exclude a range that is not part of the CIDR.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDRSet: []api.CIDRRule{{Cidr: "10.0.0.0/10", ExceptCIDRs: []api.CIDR{"10.64.0.0/11"}}},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))

	// Must have a contiguous mask, make sure Validate fails when not.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDR: []api.CIDR{"10.0.1.0/128.0.0.128"},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))

	// Prefix length must be in range for the address, make sure
	// Validate fails if given prefix length is out of range.
	err = api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{{
			IngressCommonRule: api.IngressCommonRule{
				FromCIDR: []api.CIDR{"10.0.1.0/34"},
			},
		}},
	}.Sanitize()
	c.Assert(err, Not(IsNil))
}

// Tests the restrictions of combining certain label-based L3 and L4 policies.
// This ensures that the user is informed of policy combinations that are not
// implemented in the datapath.
func (ds *PolicyTestSuite) TestEgressRuleRestrictions(c *C) {

	fooSelector := []api.EndpointSelector{
		api.NewESFromLabels(labels.ParseSelectLabel("foo")),
	}

	// Cannot combine ToEndpoints and ToCIDR
	apiRule1 := api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Egress: []api.EgressRule{
			{
				EgressCommonRule: api.EgressCommonRule{
					ToCIDR: []api.CIDR{
						"10.1.0.0/16",
						"2001:dbf::/64",
					},
					ToEndpoints: fooSelector,
				},
			},
		},
	}

	err := apiRule1.Sanitize()
	c.Assert(err, Not(IsNil))
}

func (ds *PolicyTestSuite) TestPolicyEntityValidationEgress(c *C) {
	r := api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Egress: []api.EgressRule{
			{
				EgressCommonRule: api.EgressCommonRule{
					ToEntities: []api.Entity{api.EntityWorld},
				},
			},
		},
	}
	c.Assert(r.Sanitize(), IsNil)
	c.Assert(len(r.Egress[0].ToEntities), Equals, 1)

	r.Egress[0].ToEntities = []api.Entity{api.EntityHost}
	c.Assert(r.Sanitize(), IsNil)
	c.Assert(len(r.Egress[0].ToEntities), Equals, 1)

	r.Egress[0].ToEntities = []api.Entity{"trololo"}
	c.Assert(r.Sanitize(), NotNil)
}

func (ds *PolicyTestSuite) TestPolicyEntityValidationIngress(c *C) {
	r := api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEntities: []api.Entity{api.EntityWorld},
				},
			},
		},
	}
	c.Assert(r.Sanitize(), IsNil)
	c.Assert(len(r.Ingress[0].FromEntities), Equals, 1)

	r.Ingress[0].FromEntities = []api.Entity{api.EntityHost}
	c.Assert(r.Sanitize(), IsNil)
	c.Assert(len(r.Ingress[0].FromEntities), Equals, 1)

	r.Ingress[0].FromEntities = []api.Entity{"trololo"}
	c.Assert(r.Sanitize(), NotNil)
}

func (ds *PolicyTestSuite) TestPolicyEntityValidationEntitySelectorsFill(c *C) {
	r := api.Rule{
		EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEntities: []api.Entity{api.EntityWorld, api.EntityHost},
				},
			},
		},
		Egress: []api.EgressRule{
			{
				EgressCommonRule: api.EgressCommonRule{
					ToEntities: []api.Entity{api.EntityWorld, api.EntityHost},
				},
			},
		},
	}
	c.Assert(r.Sanitize(), IsNil)
	c.Assert(len(r.Ingress[0].FromEntities), Equals, 2)
	c.Assert(len(r.Egress[0].ToEntities), Equals, 2)
}

func (ds *PolicyTestSuite) TestL3RuleLabels(c *C) {
	ruleLabels := map[string]labels.LabelArray{
		"rule0": labels.ParseLabelArray("name=apiRule0"),
		"rule1": labels.ParseLabelArray("name=apiRule1"),
		"rule2": labels.ParseLabelArray("name=apiRule2"),
	}

	rules := map[string]api.Rule{
		"rule0": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule0"],
			Ingress:          []api.IngressRule{},
			Egress:           []api.EgressRule{},
		},
		"rule1": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule1"],
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromCIDR: []api.CIDR{"10.0.1.0/32"},
					},
				},
			},
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToCIDR: []api.CIDR{"10.1.0.0/32"},
					},
				},
			},
		},
		"rule2": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule2"],
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromCIDR: []api.CIDR{"10.0.2.0/32"},
					},
				},
			},
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToCIDR: []api.CIDR{"10.2.0.0/32"},
					},
				},
			},
		},
	}

	testCases := []struct {
		description           string                           // the description to print in asserts
		rulesToApply          []string                         // the rules from the rules map to resolve, in order
		expectedIngressLabels map[string]labels.LabelArrayList // the slice of LabelArray we should see, per CIDR prefix
		expectedEgressLabels  map[string]labels.LabelArrayList // the slice of LabelArray we should see, per CIDR prefix

	}{
		{
			description:           "Empty rule that matches. Should not apply labels",
			rulesToApply:          []string{"rule0"},
			expectedIngressLabels: nil,
			expectedEgressLabels:  nil,
		},
		{
			description:           "A rule that matches. Should apply labels",
			rulesToApply:          []string{"rule1"},
			expectedIngressLabels: map[string]labels.LabelArrayList{"10.0.1.0/32": {ruleLabels["rule1"]}},
			expectedEgressLabels:  map[string]labels.LabelArrayList{"10.1.0.0/32": {ruleLabels["rule1"]}},
		}, {
			description:  "Multiple matching rules. Should apply labels from all that have rule entries",
			rulesToApply: []string{"rule0", "rule1", "rule2"},
			expectedIngressLabels: map[string]labels.LabelArrayList{
				"10.0.1.0/32": {ruleLabels["rule1"]},
				"10.0.2.0/32": {ruleLabels["rule2"]}},
			expectedEgressLabels: map[string]labels.LabelArrayList{
				"10.1.0.0/32": {ruleLabels["rule1"]},
				"10.2.0.0/32": {ruleLabels["rule2"]}},
		}}

	// endpoint selector for all tests
	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}

	for _, test := range testCases {
		finalPolicy := NewCIDRPolicy()
		for _, r := range test.rulesToApply {
			apiRule := rules[r]
			err := apiRule.Sanitize()
			c.Assert(err, IsNil, Commentf("Cannot sanitize Rule: %+v", apiRule))

			rule := &rule{Rule: apiRule}

			rule.resolveCIDRPolicy(toBar, &traceState{}, finalPolicy)
		}

		c.Assert(len(finalPolicy.Ingress.Map), Equals, len(test.expectedIngressLabels), Commentf(test.description))
		for cidrKey := range test.expectedIngressLabels {
			out := finalPolicy.Ingress.Map[cidrKey]
			c.Assert(out, Not(IsNil), Commentf(test.description))
			c.Assert(out.DerivedFromRules, checker.DeepEquals, test.expectedIngressLabels[cidrKey], Commentf(test.description))
		}

		c.Assert(len(finalPolicy.Egress.Map), Equals, len(test.expectedEgressLabels), Commentf(test.description))
		for cidrKey := range test.expectedEgressLabels {
			out := finalPolicy.Egress.Map[cidrKey]
			c.Assert(out, Not(IsNil), Commentf(test.description))
			c.Assert(out.DerivedFromRules, checker.DeepEquals, test.expectedEgressLabels[cidrKey], Commentf(test.description))
		}
	}
}

func (ds *PolicyTestSuite) TestL4RuleLabels(c *C) {
	ruleLabels := map[string]labels.LabelArray{
		"rule0": labels.ParseLabelArray("name=apiRule0"),
		"rule1": labels.ParseLabelArray("name=apiRule1"),
		"rule2": labels.ParseLabelArray("name=apiRule2"),
	}

	rules := map[string]api.Rule{
		"rule0": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule0"],
			Ingress:          []api.IngressRule{},
			Egress:           []api.EgressRule{},
		},

		"rule1": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule1"],
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{{Port: "1010", Protocol: api.ProtoTCP}},
					}},
				},
			},
			Egress: []api.EgressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{{Port: "1100", Protocol: api.ProtoTCP}},
					}},
				},
			},
		},
		"rule2": {
			EndpointSelector: api.NewESFromLabels(labels.ParseSelectLabel("bar")),
			Labels:           ruleLabels["rule2"],
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{{Port: "1020", Protocol: api.ProtoTCP}},
					}},
				},
			},
			Egress: []api.EgressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{{Port: "1200", Protocol: api.ProtoTCP}},
					}},
				},
			},
		},
	}

	testCases := []struct {
		description           string                           // the description to print in asserts
		rulesToApply          []string                         // the rules from the rules map to resolve, in order
		expectedIngressLabels map[string]labels.LabelArrayList // the slice of LabelArray we should see, in order
		expectedEgressLabels  map[string]labels.LabelArrayList // the slice of LabelArray we should see, in order

	}{
		{
			description:           "Empty rule that matches. Should not apply labels",
			rulesToApply:          []string{"rule0"},
			expectedIngressLabels: map[string]labels.LabelArrayList{},
			expectedEgressLabels:  map[string]labels.LabelArrayList{},
		},
		{
			description:           "A rule that matches. Should apply labels",
			rulesToApply:          []string{"rule1"},
			expectedIngressLabels: map[string]labels.LabelArrayList{"1010/TCP": {ruleLabels["rule1"]}},
			expectedEgressLabels:  map[string]labels.LabelArrayList{"1100/TCP": {ruleLabels["rule1"]}},
		}, {
			description:  "Multiple matching rules. Should apply labels from all that have rule entries",
			rulesToApply: []string{"rule0", "rule1", "rule2"},
			expectedIngressLabels: map[string]labels.LabelArrayList{
				"1010/TCP": {ruleLabels["rule1"]},
				"1020/TCP": {ruleLabels["rule2"]}},
			expectedEgressLabels: map[string]labels.LabelArrayList{
				"1100/TCP": {ruleLabels["rule1"]},
				"1200/TCP": {ruleLabels["rule2"]}},
		}}

	// endpoint selector for all tests
	toBar := &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	fromBar := &SearchContext{From: labels.ParseSelectLabelArray("bar")}

	for _, test := range testCases {
		finalPolicy := NewL4Policy(0)
		for _, r := range test.rulesToApply {
			apiRule := rules[r]
			err := apiRule.Sanitize()
			c.Assert(err, IsNil, Commentf("Cannot sanitize api.Rule: %+v", apiRule))

			rule := &rule{Rule: apiRule}

			rule.resolveIngressPolicy(testPolicyContext, toBar, &traceState{}, finalPolicy.Ingress, nil, nil)
			rule.resolveEgressPolicy(testPolicyContext, fromBar, &traceState{}, finalPolicy.Egress, nil, nil)
		}

		c.Assert(len(finalPolicy.Ingress), Equals, len(test.expectedIngressLabels), Commentf(test.description))
		for portProto := range test.expectedIngressLabels {
			out, found := finalPolicy.Ingress[portProto]
			c.Assert(found, Equals, true, Commentf(test.description))
			c.Assert(out, NotNil, Commentf(test.description))
			c.Assert(out.DerivedFromRules, checker.DeepEquals, test.expectedIngressLabels[portProto], Commentf(test.description))
		}

		c.Assert(len(finalPolicy.Egress), Equals, len(test.expectedEgressLabels), Commentf(test.description))
		for portProto := range test.expectedEgressLabels {
			out, found := finalPolicy.Egress[portProto]
			c.Assert(found, Equals, true, Commentf(test.description))
			c.Assert(out, Not(IsNil), Commentf(test.description))
			c.Assert(out.DerivedFromRules, checker.DeepEquals, test.expectedEgressLabels[portProto], Commentf(test.description))
		}
		finalPolicy.Detach(testSelectorCache)
	}
}

var (
	labelsA = labels.LabelArray{
		labels.NewLabel("id", "a", labels.LabelSourceK8s),
	}

	endpointSelectorA = api.NewESFromLabels(labels.ParseSelectLabel("id=a"))

	labelsB = labels.LabelArray{
		labels.NewLabel("id1", "b", labels.LabelSourceK8s),
		labels.NewLabel("id2", "c", labels.LabelSourceK8s),
	}

	labelsC = labels.LabelArray{
		labels.NewLabel("id", "c", labels.LabelSourceK8s),
	}

	endpointSelectorC = api.NewESFromLabels(labels.ParseSelectLabel("id=c"))

	ctxAToB = SearchContext{From: labelsA, To: labelsB, Trace: TRACE_VERBOSE}
	ctxAToC = SearchContext{From: labelsA, To: labelsC, Trace: TRACE_VERBOSE}
)

func expectResult(c *C, expected, obtained api.Decision, buffer *bytes.Buffer) {
	if obtained != expected {
		c.Errorf("Unexpected result: obtained=%v, expected=%v", obtained, expected)
		c.Log(buffer)
	}
}

func checkIngress(c *C, repo *Repository, ctx *SearchContext, verdict api.Decision) {
	repo.Mutex.RLock()
	defer repo.Mutex.RUnlock()

	buffer := new(bytes.Buffer)
	ctx.Logging = stdlog.New(buffer, "", 0)
	expectResult(c, verdict, repo.AllowsIngressRLocked(ctx), buffer)
}

func checkEgress(c *C, repo *Repository, ctx *SearchContext, verdict api.Decision) {
	repo.Mutex.RLock()
	defer repo.Mutex.RUnlock()

	buffer := new(bytes.Buffer)
	ctx.Logging = stdlog.New(buffer, "", 0)
	expectResult(c, verdict, repo.AllowsEgressRLocked(ctx), buffer)
}

func parseAndAddRules(c *C, rules api.Rules) *Repository {
	repo := NewPolicyRepository(nil, nil)
	repo.selectorCache = testSelectorCache

	_, _ = repo.AddList(rules)
	return repo
}

func (ds *PolicyTestSuite) TestIngressAllowAll(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorC,
			Ingress: []api.IngressRule{
				{
					// Allow all L3&L4 ingress rule
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{
							api.WildcardEndpointSelector,
						},
					},
				},
			},
		},
	})

	checkIngress(c, repo, &ctxAToB, api.Denied)
	checkIngress(c, repo, &ctxAToC, api.Allowed)

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Name: "port-90", Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC90, api.Allowed)
}

func (ds *PolicyTestSuite) TestIngressAllowAllL4Overlap(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorC,
			Ingress: []api.IngressRule{
				{
					// Allow all L3&L4 ingress rule
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{
							api.WildcardEndpointSelector,
						},
					},
				},
				{
					// This rule is a subset of the above
					// rule and should *NOT* restrict to
					// port 80 only
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC90, api.Allowed)
}

func (ds *PolicyTestSuite) TestIngressAllowAllL4OverlapNamedPort(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorC,
			Ingress: []api.IngressRule{
				{
					// Allow all L3&L4 ingress rule
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{
							api.WildcardEndpointSelector,
						},
					},
				},
				{
					// This rule is a subset of the above
					// rule and should *NOT* restrict to
					// port 80 only
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "port-80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Name: "port-80", Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC90, api.Allowed)
}

func (ds *PolicyTestSuite) TestIngressL4AllowAll(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorC,
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC90, api.Denied)

	ctxAToCNamed90 := ctxAToC
	ctxAToCNamed90.DPorts = []*models.Port{{Name: "port-90", Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToCNamed90, api.Denied)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctxAToC80)
	c.Assert(err, IsNil)

	filter, ok := l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 1)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	l4IngressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestIngressL4AllowAllNamedPort(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorC,
			Ingress: []api.IngressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "port-80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	ctxAToCNamed80 := ctxAToC
	ctxAToCNamed80.DPorts = []*models.Port{{Name: "port-80", Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToCNamed80, api.Allowed)

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC80, api.Denied)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToC90, api.Denied)

	ctxAToCNamed90 := ctxAToC
	ctxAToCNamed90.DPorts = []*models.Port{{Name: "port-90", Protocol: models.PortProtocolTCP}}
	checkIngress(c, repo, &ctxAToCNamed90, api.Denied)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctxAToCNamed80)
	c.Assert(err, IsNil)

	filter, ok := l4IngressPolicy["port-80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 0)
	c.Assert(filter.PortName, Equals, "port-80")
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 1)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	l4IngressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestEgressAllowAll(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEndpoints: []api.EndpointSelector{
							api.WildcardEndpointSelector,
						},
					},
				},
			},
		},
	})

	checkEgress(c, repo, &ctxAToB, api.Allowed)
	checkEgress(c, repo, &ctxAToC, api.Allowed)

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToC90, api.Allowed)
}

func (ds *PolicyTestSuite) TestEgressL4AllowAll(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	ctxAToC80 := ctxAToC
	ctxAToC80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToC80, api.Allowed)

	ctxAToC90 := ctxAToC
	ctxAToC90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToC90, api.Denied)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{From: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4EgressPolicy, err := repo.ResolveL4EgressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := l4EgressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, false)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 1)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	l4EgressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestEgressL4AllowWorld(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEntities: []api.Entity{api.EntityWorld},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	worldLabel := labels.ParseSelectLabelArray("reserved:world")
	ctxAToWorld80 := SearchContext{From: labelsA, To: worldLabel, Trace: TRACE_VERBOSE}
	ctxAToWorld80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld80, api.Allowed)

	ctxAToWorld90 := ctxAToWorld80
	ctxAToWorld90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld90, api.Denied)

	// Pod to pod must be denied on port 80 and 90, only world was whitelisted
	fooLabel := labels.ParseSelectLabelArray("k8s:app=foo")
	ctxAToFoo := SearchContext{From: labelsA, To: fooLabel, Trace: TRACE_VERBOSE,
		DPorts: []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}}
	checkEgress(c, repo, &ctxAToFoo, api.Denied)
	ctxAToFoo90 := ctxAToFoo
	ctxAToFoo90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToFoo90, api.Denied)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{From: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4EgressPolicy, err := repo.ResolveL4EgressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := l4EgressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, false)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 1)
	l4EgressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestEgressL4AllowAllEntity(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEntities: []api.Entity{api.EntityAll},
					},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		},
	})

	worldLabel := labels.ParseSelectLabelArray("reserved:world")
	ctxAToWorld80 := SearchContext{From: labelsA, To: worldLabel, Trace: TRACE_VERBOSE}
	ctxAToWorld80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld80, api.Allowed)

	ctxAToWorld90 := ctxAToWorld80
	ctxAToWorld90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld90, api.Denied)

	// Pod to pod must be allowed on port 80, denied on port 90 (all identity)
	fooLabel := labels.ParseSelectLabelArray("k8s:app=foo")
	ctxAToFoo := SearchContext{From: labelsA, To: fooLabel, Trace: TRACE_VERBOSE,
		DPorts: []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}}
	checkEgress(c, repo, &ctxAToFoo, api.Allowed)
	ctxAToFoo90 := ctxAToFoo
	ctxAToFoo90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToFoo90, api.Denied)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{From: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4EgressPolicy, err := repo.ResolveL4EgressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := l4EgressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, false)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 1)
	l4EgressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestEgressL3AllowWorld(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEntities: []api.Entity{api.EntityWorld},
					},
				},
			},
		},
	})

	worldLabel := labels.ParseSelectLabelArray("reserved:world")
	ctxAToWorld80 := SearchContext{From: labelsA, To: worldLabel, Trace: TRACE_VERBOSE}
	ctxAToWorld80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld80, api.Allowed)

	ctxAToWorld90 := ctxAToWorld80
	ctxAToWorld90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld90, api.Allowed)

	// Pod to pod must be denied on port 80 and 90, only world was whitelisted
	fooLabel := labels.ParseSelectLabelArray("k8s:app=foo")
	ctxAToFoo := SearchContext{From: labelsA, To: fooLabel, Trace: TRACE_VERBOSE,
		DPorts: []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}}
	checkEgress(c, repo, &ctxAToFoo, api.Denied)
	ctxAToFoo90 := ctxAToFoo
	ctxAToFoo90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToFoo90, api.Denied)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{From: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)
}

func (ds *PolicyTestSuite) TestEgressL3AllowAllEntity(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					EgressCommonRule: api.EgressCommonRule{
						ToEntities: []api.Entity{api.EntityAll},
					},
				},
			},
		},
	})

	worldLabel := labels.ParseSelectLabelArray("reserved:world")
	ctxAToWorld80 := SearchContext{From: labelsA, To: worldLabel, Trace: TRACE_VERBOSE}
	ctxAToWorld80.DPorts = []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld80, api.Allowed)

	ctxAToWorld90 := ctxAToWorld80
	ctxAToWorld90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToWorld90, api.Allowed)

	// Pod to pod must be allowed on both port 80 and 90 (L3 only rule)
	fooLabel := labels.ParseSelectLabelArray("k8s:app=foo")
	ctxAToFoo := SearchContext{From: labelsA, To: fooLabel, Trace: TRACE_VERBOSE,
		DPorts: []*models.Port{{Port: 80, Protocol: models.PortProtocolTCP}}}
	checkEgress(c, repo, &ctxAToFoo, api.Allowed)
	ctxAToFoo90 := ctxAToFoo
	ctxAToFoo90.DPorts = []*models.Port{{Port: 90, Protocol: models.PortProtocolTCP}}
	checkEgress(c, repo, &ctxAToFoo90, api.Allowed)

	buffer := new(bytes.Buffer)
	ctx := SearchContext{From: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)
}

func (ds *PolicyTestSuite) TestL4WildcardMerge(c *C) {

	// First, test implicit case.
	//
	// Test the case where if we have rules that select the same endpoint on the
	// same port-protocol tuple with one that is L4-only, and the other applying
	// at L4 and L7, that the L4-only rule shadows the L4-L7 rule. This is because
	// L4-only rule implicitly allows all traffic at L7, so the L7-related
	// parts of the L4-L7 rule are useless.
	repo := parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
			{
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "7000", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						L7Proto: "testparser",
						L7: []api.PortRuleL7{
							{"Key": "Value"},
						},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "7000", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	expected := &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "http",
		L7RulesPerSelector: L7DataMap{
			wildcardCachedSelector: nil,
			cachedSelectorC: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	buffer := new(bytes.Buffer)
	ctx := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter.L7RulesPerSelector[cachedSelectorC], Not(IsNil))
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	c.Assert(filter, checker.Equals, expected)
	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)

	expectedL7 := &L4Filter{
		Port: 7000, Protocol: api.ProtoTCP, U8Proto: 6,
		L7Parser: "testparser",
		L7RulesPerSelector: L7DataMap{
			cachedSelectorC: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					L7Proto: "testparser",
					L7:      []api.PortRuleL7{{"Key": "Value"}, {}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	filterL7, ok := l4IngressPolicy["7000/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filterL7.Port, Equals, 7000)
	c.Assert(filterL7.Ingress, Equals, true)

	c.Assert(len(filterL7.L7RulesPerSelector), Equals, 1)
	c.Assert(filterL7.L7RulesPerSelector[cachedSelectorC], Not(IsNil))
	c.Assert(filterL7.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	c.Assert(filterL7, checker.Equals, expectedL7)
	c.Assert(filterL7.L7Parser, Equals, L7ParserType("testparser"))

	l4IngressPolicy.Detach(repo.GetSelectorCache())

	// Test the reverse order as well; ensure that we check both conditions
	// for if L4-only policy is in the L4Filter for the same port-protocol tuple,
	// and L7 metadata exists in the L4Filter we are adding; expect to resolve
	// to L4-only policy without any L7-metadata.
	repo = parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "7000", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "7000", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						L7Proto: "testparser",
						L7: []api.PortRuleL7{
							{"Key": "Value"},
						},
					},
				}},
			},
			{
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err = repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok = l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	c.Assert(filter.L7RulesPerSelector[cachedSelectorC], Not(IsNil))
	c.Assert(filter, checker.Equals, expected)
	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)

	filterL7, ok = l4IngressPolicy["7000/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filterL7.Port, Equals, 7000)
	c.Assert(filterL7.Ingress, Equals, true)

	c.Assert(len(filterL7.L7RulesPerSelector), Equals, 1)
	c.Assert(filterL7.L7RulesPerSelector[cachedSelectorC], Not(IsNil))
	c.Assert(filterL7.L7RulesPerSelector[wildcardCachedSelector], IsNil)
	c.Assert(filterL7, checker.Equals, expectedL7)
	c.Assert(filterL7.L7Parser, Equals, L7ParserType("testparser"))

	l4IngressPolicy.Detach(repo.GetSelectorCache())

	// Second, test the explicit allow at L3.
	repo = parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err = repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok = l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)
	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter, checker.Equals, expected)
	l4IngressPolicy.Detach(repo.GetSelectorCache())

	// Test the reverse order as well; ensure that we check both conditions
	// for if L4-only policy is in the L4Filter for the same port-protocol tuple,
	// and L7 metadata exists in the L4Filter we are adding; expect to resolve
	// to L4-only policy without any L7-metadata.
	repo = parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err = repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok = l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)
	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter, checker.Equals, expected)
	l4IngressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestL3L4L7Merge(c *C) {

	// First rule allows ingress from all endpoints to port 80 only on
	// GET to "/". However, second rule allows all traffic on port 80 only to a
	// specific endpoint. When these rules are merged, it equates to allowing
	// all traffic from port 80 from any endpoint.
	//
	// TODO: This comment can't be correct, the resulting policy
	// should allow all on port 80 only from endpoint C, traffic
	// from all other endpoints should still only allow only GET
	// on "/".
	repo := parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	buffer := new(bytes.Buffer)
	ctx := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], Not(IsNil))
	c.Assert(filter.L7RulesPerSelector[cachedSelectorC], IsNil)

	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)
	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter, checker.Equals, &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "http",
		L7RulesPerSelector: L7DataMap{
			cachedSelectorC: nil,
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	})
	l4IngressPolicy.Detach(repo.GetSelectorCache())

	repo = parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				IngressCommonRule: api.IngressCommonRule{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
				},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = stdlog.New(buffer, "", 0)

	l4IngressPolicy, err = repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok = l4IngressPolicy["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)
	c.Assert(len(filter.L7RulesPerSelector), Equals, 2)
	c.Assert(filter.L7RulesPerSelector[wildcardCachedSelector], Not(IsNil))
	c.Assert(filter.L7RulesPerSelector[cachedSelectorC], IsNil)
	c.Assert(filter, checker.Equals, &L4Filter{
		Port: 80, Protocol: api.ProtoTCP, U8Proto: 6,
		wildcard: wildcardCachedSelector,
		L7Parser: "http",
		L7RulesPerSelector: L7DataMap{
			cachedSelectorC: nil,
			wildcardCachedSelector: &PerSelectorPolicy{
				L7Rules: api.L7Rules{
					HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
				},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	})

	l4IngressPolicy.Detach(repo.GetSelectorCache())
}

func (ds *PolicyTestSuite) TestMatches(c *C) {
	repo := parseAndAddRules(c, api.Rules{
		&api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{endpointSelectorC},
					},
				},
			},
		},
		&api.Rule{
			NodeSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					IngressCommonRule: api.IngressCommonRule{
						FromEndpoints: []api.EndpointSelector{endpointSelectorC},
					},
				},
			},
		},
	})

	epRule := repo.rules[0]
	hostRule := repo.rules[1]

	selectedEpLabels := labels.ParseSelectLabel("id=a")
	selectedIdentity := identity.NewIdentity(54321, labels.Labels{selectedEpLabels.Key: selectedEpLabels})

	notSelectedEpLabels := labels.ParseSelectLabel("id=b")
	notSelectedIdentity := identity.NewIdentity(9876, labels.Labels{notSelectedEpLabels.Key: notSelectedEpLabels})

	hostLabels := labels.Labels{selectedEpLabels.Key: selectedEpLabels}
	hostLabels.MergeLabels(labels.LabelHost)
	hostIdentity := identity.NewIdentity(identity.ReservedIdentityHost, hostLabels)

	// notSelectedEndpoint is not selected by rule, so we it shouldn't be added
	// to EndpointsSelected.
	c.Assert(epRule.matches(notSelectedIdentity), Equals, false)
	c.Assert(epRule.metadata.IdentitySelected, checker.DeepEquals, map[identity.NumericIdentity]bool{notSelectedIdentity.ID: false})

	// selectedEndpoint is selected by rule, so we it should be added to
	// EndpointsSelected.
	c.Assert(epRule.matches(selectedIdentity), Equals, true)
	c.Assert(epRule.metadata.IdentitySelected, checker.DeepEquals, map[identity.NumericIdentity]bool{selectedIdentity.ID: true, notSelectedIdentity.ID: false})

	// Test again to check for caching working correctly.
	c.Assert(epRule.matches(selectedIdentity), Equals, true)
	c.Assert(epRule.metadata.IdentitySelected, checker.DeepEquals, map[identity.NumericIdentity]bool{selectedIdentity.ID: true, notSelectedIdentity.ID: false})

	// Possible scenario where an endpoint is deleted, and soon after another
	// endpoint is added with the same ID, but with a different identity. Matching
	// needs to handle this case correctly.
	c.Assert(epRule.matches(notSelectedIdentity), Equals, false)
	c.Assert(epRule.metadata.IdentitySelected, checker.DeepEquals, map[identity.NumericIdentity]bool{selectedIdentity.ID: true, notSelectedIdentity.ID: false})

	// host endpoint is not selected by rule, so we it shouldn't be added to EndpointsSelected.
	c.Assert(epRule.matches(hostIdentity), Equals, false)
	c.Assert(epRule.metadata.IdentitySelected, checker.DeepEquals,
		map[identity.NumericIdentity]bool{selectedIdentity.ID: true, notSelectedIdentity.ID: false, hostIdentity.ID: false})

	// selectedEndpoint is not selected by rule, so we it shouldn't be added to EndpointsSelected.
	c.Assert(hostRule.matches(selectedIdentity), Equals, false)
	c.Assert(hostRule.metadata.IdentitySelected, checker.DeepEquals, map[identity.NumericIdentity]bool{selectedIdentity.ID: false})

	// host endpoint is selected by rule, so we it should be added to EndpointsSelected.
	c.Assert(hostRule.matches(hostIdentity), Equals, true)
	c.Assert(hostRule.metadata.IdentitySelected, checker.DeepEquals,
		map[identity.NumericIdentity]bool{selectedIdentity.ID: false, hostIdentity.ID: true})
}
