package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	//log "wenscan/Log"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/thoas/go-funk"
)

var hosturl = "http://localhost/"
var hostlogin = "login.php"
var hostlogout = "logout.php"
var removeAttribute = `var itags = document.getElementsByTagName('input');for(i=0;i<=itags.length;i++){if(itags[i]){itags[i].removeAttribute('style')}}`
var fetchok bool

const (
	inViewportJS = `(function(a) {
		var r = a[0].getBoundingClientRect();
		return r.top >= 0 && r.left >= 0 && r.bottom <= window.innerHeight && r.right <= window.innerWidth;
	})($x('%s'))`
	level         = 3 //网页抓取深度页数
	sethreftarget = `atags = document.getElementsByTagName('a');for(i=0;i<=atags.length;i++) { if(atags[i]){atags[i].setAttribute('target', '')}}`
)

// type chromecontext struct {
// 	Ctx      context.Context
// 	Cancel   context.CancelFunc
// 	Requests []reqinfo
// }

type reqinfo struct {
	Method  string
	URL     string
	Hreader string
}

//Spider 爬虫资源，设计目的是爬网页，注意使用此结构的函数在多线程中没上锁是不安全的，理想状态为一条线程使用这个结构
type Spider struct {
	Parentcxt context.Context //主context
	Requests  []reqinfo
	//Chromecontext []*chromecontext //存储着主context子tag页面
	Responses     chan []map[string]string
	ReqMode       string
	nodes         []*cdp.Node // 定义全局变量，用来保存爬虫的数据node
	ExecAllocator []func(*chromedp.ExecAllocator)
	ch            chan bool
}

// func (chromecontext *chromecontext) Close() {
// 	defer (chromecontext.Cancel)()
// 	defer chromedp.Cancel(chromecontext.Ctx)
// }

//CheckPayloadbyConsoleLog 检测回复中的log是否有我们触发的payload
func (spider *Spider) CheckPayloadbyConsole(types string, xsschecker string) bool {
	select {
	case responseS := <-spider.Responses:
		for _, response := range responseS {
			if v, ok := response[types]; ok {
				if v == xsschecker {
					return true
				}
			}
		}
	case <-time.After(time.Duration(5) * time.Second):
		return false
	}
	return false
}

/*
//post请求方式
switch ev := ev.(type) {
		case *fetch.EventRequestPaused:
			go func() {
				c := chromedp.FromContext(cctx)
				cctx := cdp.WithExecutor(cctx, c.Target)
                newreq := fetch.ContinueRequest(ev.RequestID)
				newreq.URL = "http://my-vps:4321/fse/ooo.php"
				newreq.Method = "POST"
				newreq.Headers = []*fetch.HeaderEntry{{"Content-Type", "application/x-www-form-urlencoded"}}
				newreq.PostData = base64.StdEncoding.EncodeToString([]byte("aaaaa=1234"))
				newreq.Do(cctx)
        	}()
}
*/

//ListenTarget
func (spider *Spider) ListenTarget(ctx context.Context, extends interface{}) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		Response := make(map[string]string)
		Responses := []map[string]string{}
		switch ev := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			//fmt.Printf("* console.%s call:\n", ev.Type)
			for _, arg := range ev.Args {
				//fmt.Printf("%s - %s\n", arg.Type, string(arg.Value))
				Response[string(ev.Type)] = strings.ReplaceAll(string(arg.Value), "\"", "")
				Responses = append(Responses, Response)
			}
			// go func() {
			// 	spider.Responses <- Responses
			// }()
		case *runtime.EventExceptionThrown:
			//s := ev.ExceptionDetails.Error()
			//fmt.Printf("* %s\n", s)
		case *fetch.EventRequestPaused:
			//log.Printf("fetch.EventRequestPaused:%v \n", ev)
			go func(ctx context.Context, ev *fetch.EventRequestPaused) {
				var a chromedp.Action
				if strings.HasSuffix(ev.Request.URL, "login.php") ||
					strings.HasSuffix(ev.Request.URL, "index.php") ||
					strings.HasSuffix(ev.Request.URL, ".css") ||
					strings.HasSuffix(ev.Request.URL, ".js") {
					//log.Println(ev.Request.Method, " ContinueRequest:", ev.Request.URL)
					a = fetch.ContinueRequest(ev.RequestID)
				} else {
					a = fetch.FailRequest(ev.RequestID, network.ErrorReasonAborted)
					//log.Println(ev.Request.Method, " EventRequestPaused:", ev.Request.URL)
				}
				var req reqinfo
				req.URL = ev.Request.URL
				req.Method = ev.Request.Method
				if !strings.HasSuffix(ev.Request.URL, "login.php") &&
					!strings.HasSuffix(ev.Request.URL, ".css") &&
					!strings.HasSuffix(ev.Request.URL, ".js") &&
					!strings.HasSuffix(ev.Request.URL, ".ico") {
					if !funk.Contains(spider.Requests, req) {
						spider.Requests = append(spider.Requests, req)
						log.Println("Add crawer url:", req)
					} else {
						//log.Println("The url is exist:", req)
					}
				}
				if err := chromedp.Run(ctx, a); err != nil {
					log.Println("ListenTarget error", err)
				}
			}(ctx, ev)
		case *page.EventJavascriptDialogOpening:
			log.Println("EventJavascriptDialogOpening url:", ev.URL)
		case *page.EventNavigatedWithinDocument:
			log.Println("EventNavigatedWithinDocument url:", ev.URL)
		case *page.EventWindowOpen:
			log.Println("EventWindowOpen url:", ev.URL)
			var req reqinfo
			req.URL = ev.URL
			req.Method = "GET"
			if !strings.HasSuffix(ev.URL, "login.php") &&
				!strings.HasSuffix(ev.URL, ".css") &&
				!strings.HasSuffix(ev.URL, ".js") &&
				!strings.HasSuffix(ev.URL, ".ico") {
				if !funk.Contains(spider.Requests, req) {
					spider.Requests = append(spider.Requests, req)
					log.Println("EventWindowOpen Add crawer url:", req)
				} else {
					//log.Println("The url is exist:", req)
				}
			}
		case *page.EventDocumentOpened:
			log.Println("EventDocumentOpened url:", ev.Frame.URL)
		case *network.EventRequestWillBeSentExtraInfo:

		case *network.EventRequestWillBeSent:
			//重定向
			request := ev
			//fmt.Printf(" request url: %s\n", request.DocumentURL)
			if ev.RedirectResponse != nil {
				//url = request.DocumentURL
				fmt.Printf("链接 %s: 重定向: %s\n", request.DocumentURL, request.RedirectResponse.URL)
			}
		case *network.EventResponseReceived:
			// go func() {
			// 	resp := ev
			// 	byte, err := fetch.GetResponseBody(fetch.RequestID(resp.RequestID)).Do(Chromectx.Ctx)
			// 	if err != nil {
			// 		log.Println(err.Error())
			// 	}
			// 	fmt.Println(byte)
			// }()

			// resp := ev.Response
			// if len(resp.Headers) != 0 {
			// 	log.Printf("received headers: %s", resp.)
			// }
		case *network.EventResponseReceivedExtraInfo:
		case *dom.EventSetChildNodes:
		case *page.EventLoadEventFired:
			// log.Println("page.EventLoadEventFired")
			// go func() {
			// 	spider.ch <- true
			// }()
		case *page.EventFrameRequestedNavigation:
			// log.Printf("开始请求的导航 FrameID:%s url %s , 导航类型 type: %s  导航请求理由：%s ",
			// 	ev.FrameID, ev.URL, ev.Disposition, ev.Reason)
		case *page.EventFrameStoppedLoading:

		}

	})
}

func (spider *Spider) Init() {
	spider.Responses = make(chan []map[string]string)
	options := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		//chromedp.Flag("disable-images", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-xss-auditor", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("allow-running-insecure-content", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("disable-webgl", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("block-new-web-contents", true),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.UserAgent(`Mozilla/5.0 (Windows NT 6.3; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/73.0.3683.103 Safari/537.36`),
	}
	spider.ExecAllocator = append(chromedp.DefaultExecAllocatorOptions[:], options...)
	//NewExecAllocator 是新建一个浏览器
	c, _ := chromedp.NewExecAllocator(context.Background(), spider.ExecAllocator...)
	spider.Parentcxt = c
	spider.ch = make(chan bool)
}

//Crawler 爬取链接
func (spider *Spider) Crawler(url string, function interface{}) (context.Context, context.CancelFunc, error) {
	var ctx context.Context
	var cctx context.Context
	var cancel context.CancelFunc
	var err error
	//新建一个标签页
	if v, ok := function.(context.Context); ok {
		c := chromedp.FromContext(v)
		cctx = cdp.WithExecutor(v, c.Target)
		ctx = cctx
	} else {
		ctx, cancel = chromedp.NewContext(spider.Parentcxt)
	}

	if _, ok := function.(context.Context); !ok {
		spider.ListenTarget(ctx, spider.Requests)
		err = chromedp.Run(
			ctx,
			//开启拦截请求
			fetch.Enable(),
			//导航
			chromedp.Navigate(url),
			// 获取获取服务列表HTML
			spider.GetPageInfomation(),
		)
	} else {

		ctx11, cancel := chromedp.NewContext(ctx)
		defer cancel()
		spider.ListenTarget(ctx11, spider.Requests)
		err = chromedp.Run(
			ctx11,
			chromedp.Navigate(url),
		)
		_ctx, _cancel := context.WithTimeout(ctx11, 3*time.Second)
		defer _cancel()
		//spider.ListenTarget(_ctx, spider.Requests)
		err = chromedp.Run(
			_ctx,
			fetch.Enable(),
			spider.CommitBybut(),
		)
		if err != nil {
			//log.Println(err.Error())
		}
		newctx, newcancel := context.WithTimeout(ctx11, 3*time.Second)
		defer newcancel()
		//spider.ListenTarget(newctx, spider.Requests)
		err = chromedp.Run(
			newctx,
			fetch.Enable(),
			spider.ChlikByLink(),
		)
		if err != nil {
			//log.Println(err.Error())
		}
	}

	return ctx, cancel, err
}

func ShowCookies() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetAllCookies().Do(ctx)
		if err != nil {
			return err
		}
		for i, cookie := range cookies {
			log.Printf("chrome cookie %d: %+v", i, cookie)
		}
		return nil
	})
}

func SetCookie(name, value, domain, path string, httpOnly, secure bool) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
		network.SetCookie(name, value).
			WithExpires(&expr).
			WithDomain(domain).
			WithPath(path).
			WithHTTPOnly(httpOnly).
			WithSecure(secure).
			Do(ctx)
		return nil
	})
}

//GetPageInfomation 获取网页信息
func (spider *Spider) GetPageInfomation() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		//var res string
		if err := chromedp.WaitReady(`body`, chromedp.ByQuery).Do(ctx); err != nil {
			return err
		}
		if err := spider.CommitBybutton(ctx); err != nil {
			return err
		}
		chromedp.WaitReady(`a`).Do(ctx)
		if err := spider.ChlikLink(ctx); err != nil {
			return err
		}
		return nil
	})
}

//CommitBybut 封装提交接口
func (spider *Spider) CommitBybut() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		//PrintHtml(ctx)
		if err := spider.CommitBybutton(ctx); err != nil {
			return err
		}
		return nil
	})
}

//PrintHtml 打印当前html
func PrintHtml(ctx context.Context) error {
	var html string
	err := chromedp.OuterHTML("body", &html, chromedp.BySearch).Do(ctx)
	if err != nil {
		log.Println("PrintHtml error:", err.Error())
	}
	fmt.Println(html)
	return err
}

//ChlikByLink 封装提交链接接口
func (spider *Spider) ChlikByLink() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		//
		//chromedp.Sleep(1 * time.Second)
		if err := spider.ChlikLink(ctx); err != nil {
			return err
		}
		return nil
	})
}

//CommitBybutton 提交按钮
func (spider *Spider) CommitBybutton(ctx context.Context) error {
	var nodes []*cdp.Node
	err := chromedp.Nodes("//input[@type='submit']", &nodes, chromedp.BySearch).Do(ctx)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		log.Printf("no find //input[@type='submit'] node")
		return nil
	}
	for _, node := range nodes {
		//自动填写表单
		spider.fillForm(ctx)
		if !(node.AttributeValue("type") == "hidden" || node.AttributeValue("display") == "none") {
			//鼠标移动到button上
			err := chromedp.MouseClickNode(node, chromedp.ButtonType(input.Left)).Do(ctx)
			if err != nil {
				//log.Println("CommitBybutton MouseClickNode error:", err)
			}
			chromedp.Sleep(1 * time.Second)
		}
	}

	return nil
}

//fillForm 填写表单
func (spider *Spider) fillForm(ctx context.Context) error {
	var nodes []*cdp.Node
	//var res string
	//获取 input节点
	err := chromedp.Nodes("//input", &nodes).Do(ctx)
	if err != nil {
		fmt.Println("fillForm error: ", err)
	}
	if len(nodes) == 0 {
		return errors.New("no find node")
	}
	//移除input的 style 属性
	err = chromedp.Evaluate(removeAttribute, nil).Do(ctx)
	if err != nil {
		log.Fatal("removeAttribute error: ", err)
	}

	for _, node := range nodes {
		var ok bool
		chromedp.EvaluateAsDevTools(fmt.Sprintf(inViewportJS, node.FullXPath()), &ok).Do(ctx)
		if err != nil {
			log.Fatal("got  error:", err)
		}
		if !(node.AttributeValue("type") == "hidden" || node.AttributeValue("display") == "none") {
			//fmt.Println(node.Attributes)
			//填写用户名
			for _, name := range []string{"user", "用户名", "username"} {
				if v := node.AttributeValue("name"); name == v {
					//移动鼠标到目标节点上,模拟人类操作，也许会触发标签事件
					err = chromedp.MouseClickNode(node, chromedp.ButtonType(input.Left)).Do(ctx)
					if err != nil {
						log.Fatal("MouseClickNode error:", err)
					}
					err = chromedp.SendKeys(fmt.Sprintf(`input[name=%s]`, v), "admin").Do(ctx)
					if err != nil {
						log.Fatal("SendKeys user name error:", err)
					}
				}
			}
			//填写密码
			for _, name := range []string{"pwd", "密码", "pass", "password"} {
				if v := node.AttributeValue("name"); name == v {
					//移动鼠标到目标节点上,模拟人类操作，也许会触发标签事件
					err = chromedp.MouseClickNode(node, chromedp.ButtonType(input.Left)).Do(ctx)
					if err != nil {
						log.Fatal("MouseClickNode error:", err)
					}
					//println(node.FullXPath())
					err = chromedp.SendKeys(fmt.Sprintf(`input[name=%s]`, v), "password").Do(ctx)
					if err != nil {
						log.Fatal("SendKeys user name error:", err)
					}
				}
			}
		}

	}
	return err
}

//ChlikLink 点击a标签
func (spider *Spider) ChlikLink(ctx context.Context) error {
	var nodes []*cdp.Node
	if err := chromedp.WaitReady(`a`).Do(ctx); err != nil {
		log.Println("WaitReady error: ", err)
		return err
	}

	err := chromedp.Nodes("//a", &nodes).Do(ctx)
	if err != nil {
		log.Println("ChlikLink error: ", err)
		return err
	}
	if len(nodes) == 0 {
		return errors.New("no find node")
	}
	chromedp.Sleep(time.Second)
	for _, link := range nodes {
		if err := chromedp.Evaluate(sethreftarget, nil).Do(ctx); err != nil {
			//log.Println("sethreftarget error: ", err)
		}
		if !(link.AttributeValue("type") == "hidden" || link.AttributeValue("display") == "none") {
			err := chromedp.MouseClickNode(link, chromedp.ButtonLeft).Do(ctx)
			if err != nil {
				//log.Println("ChlikLink MouseClickNode error: ", err)
			}
		}
	}

	return err
}

func main() {
	Spider := Spider{}
	Spider.Init()
	if ctx, cancel, err := Spider.Crawler(`http://localhost/login.php`, nil); err != nil {
		defer cancel()
	} else {
		for _, Requests := range Spider.Requests {
			//log.Println("request url:", Requests.URL)
			if _, ok := ctx.Deadline(); ok {
				log.Println("Ctx is Deadline")
				break
			}
			if _, _, err := Spider.Crawler(Requests.URL, ctx); err != nil {
				log.Println("Crawler error:", err)
			} else {
				//log.Println(CContext.Requests)
			}
		}
		log.Println("program quit:", err)
	}
	// 	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// 		log.Println("request:", r.URL)
	// 		if r.URL.Path == "/index" {
	// 			fmt.Fprintf(w, `
	// <html>
	// <body>
	// <a class="nav" href="/link">link</a>
	// <a class="nav" href="/link_blank" target="_blank">link_blank</a>
	// <form id="form_post" action="/form_post">
	//     <input class="nav" type="submit">
	// </form>
	// <form id="form_get" action="/form_get" method="get">
	//     <input class="nav" type="submit">
	// </form>
	// <form id="form_post_blank" action="/form_post_blank" target="_blank">
	//     <input class="nav" type="submit">
	// </form>
	// <form id="form_get_blank" action="/form_get_blank" method="get" target="_blank">
	//     <input class="nav" type="submit">
	// </form>
	// <script>
	//     setTimeout(() => {
	//         window.open('/window.open');
	//     }, 10);
	//     setTimeout(() => {
	//         window.location.href = '/window.location';
	//     }, 20);
	// </script>
	// </body>
	// </html>`)
	// 		}
	// 	}))
	// 	defer s.Close()

	// 	opts := append(chromedp.DefaultExecAllocatorOptions[:],
	// 		// There are two kinds of navigations:
	// 		// 1) navigate in the same page;
	// 		// 2) navigate to a new tab.
	// 		// Case one can be blocked by the "fetch.FailRequest".
	// 		// Case two can be blocked with browser command line arguments: block-new-web-contents
	// 		chromedp.Flag("block-new-web-contents", true),
	// 		// If headless mode is disabled, case two will send the request to the server,
	// 		// but the page is still blocked.
	// 		//chromedp.Flag("headless", false),
	// 	)
	// 	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	// 	defer cancel()

	// 	ctx, cancel = chromedp.NewContext(ctx) //chromedp.WithDebugf(log.Printf),

	// 	defer cancel()

	// 	chromedp.ListenTarget(ctx, func(ev interface{}) {
	// 		switch ev := ev.(type) {
	// 		case *fetch.EventRequestPaused:
	// 			go func(ctx context.Context, ev *fetch.EventRequestPaused) {
	// 				var a chromedp.Action
	// 				// You need to decide which requests not to block.
	// 				if strings.HasSuffix(ev.Request.URL, "/index") {
	// 					a = fetch.ContinueRequest(ev.RequestID)
	// 				} else {
	// 					log.Println("EventRequestPaused:", ev.Request.URL)
	// 					a = fetch.FailRequest(ev.RequestID, network.ErrorReasonAborted)
	// 				}
	// 				if err := chromedp.Run(ctx, a); err != nil {
	// 					log.Println(err)
	// 				}
	// 			}(ctx, ev)
	// 		case *page.EventWindowOpen:
	// 			log.Println("EventWindowOpen:", ev.URL)
	// 		}
	// 	})

	// 	if err := chromedp.Run(ctx,
	// 		fetch.Enable(),
	// 		chromedp.Navigate(s.URL+"/index"),
	// 		chromedp.QueryAfter(".nav", func(ctx context.Context, id runtime.ExecutionContextID, nodes ...*cdp.Node) error {
	// 			for _, n := range nodes {
	// 				// Let's just ignore errors here.
	// 				_ = chromedp.MouseClickNode(n).Do(ctx)
	// 			}
	// 			return nil
	// 		}, chromedp.ByQueryAll),
	// 	); err != nil {
	// 		log.Println(err)
	// 	}

	// 	time.Sleep(2 * time.Second)

}
