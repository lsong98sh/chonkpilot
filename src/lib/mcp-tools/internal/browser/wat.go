package browser

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// stepWAT WAT：显式等待（唯一会等待的指令，超时默认 wat_timeout_ms / 10s）。
//
//	WAT <loc> visible [timeoutMs]        # 等元素可见
//	WAT <loc> enabled [timeoutMs]        # 等元素可交互
//	WAT <loc> text "x" [timeoutMs]       # 等元素文本 == x
//	WAT netidle [timeoutMs]              # 网络空闲（近似）
//	WAT load [timeoutMs]                 # 页面加载完成
func (r *Runner) stepWAT(st *Step) error {
	if len(st.Args) == 0 {
		return stepErr(st.Line, st.Raw, "syntax", "WAT 需要参数")
	}
	timeout := r.opt.WatTimeoutMs
	if timeout <= 0 {
		timeout = 10000
	}
	// 末尾数字参数 = 超时覆盖
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	if n := len(st.Args); n >= 2 {
		if t, err := strconv.Atoi(st.Args[n-1]); err == nil {
			deadline = time.Now().Add(time.Duration(t) * time.Millisecond)
		}
	}

	first := st.Args[0]
	low := strings.ToLower(first)
	switch low {
	case "netidle":
		return r.waitLoad(st, true, deadline)
	case "load":
		return r.waitLoad(st, false, deadline)
	}

	loc := first
	mode := "visible"
	want := ""
	if len(st.Args) >= 2 {
		mode = strings.ToLower(st.Args[1])
	}
	// text 模式的期望文本：跳过末尾数字参数找双引号文本（Args 已解引号）
	if mode == "text" {
		for _, a := range st.Args[2:] {
			if _, err := strconv.Atoi(a); err != nil {
				want = a
				break
			}
		}
	}
	switch mode {
	case "visible", "enabled":
	default:
		return stepErr(st.Line, st.Raw, "syntax", "WAT 仅支持 visible|enabled|text|netidle|load")
	}

	for {
		ok, val, err := r.probe(r.ctx, loc, mode, "")
		if err == nil {
			if mode == "visible" && ok && truthy(val) {
				return nil
			}
			if mode == "enabled" && ok && truthy(val) {
				return nil
			}
			if mode == "text" && ok && anyString(val) == want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			got := "未出现"
			if ok {
				got = fmt.Sprintf("%v", val)
			}
			return stepErr(st.Line, st.Raw, "timeout", "等待超时: "+loc+" "+mode+"（当前 "+got+"）")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// waitLoad 轮询加载状态；netidle 用资源计数两拍静止近似。
func (r *Runner) waitLoad(st *Step, netidle bool, deadline time.Time) error {
	prev := -1
	for {
		var state string
		if err := r.evalJS(r.ctx, `document.readyState`, &state); err != nil {
			return stepErr(st.Line, st.Raw, "js", "检查页面状态失败: "+err.Error())
		}
		complete := state == "complete"
		if !netidle && complete {
			return nil
		}
		if netidle && complete {
			var n int
			_ = r.evalJS(r.ctx, `performance.getEntriesByType('resource').length`, &n)
			if prev >= 0 && n == prev {
				return nil
			}
			prev = n
		}
		if time.Now().After(deadline) {
			return stepErr(st.Line, st.Raw, "timeout", "页面加载等待超时（state="+state+"）")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func anyString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
