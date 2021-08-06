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
)

var hosturl = "http://localhost/"
var hostlogin = "login.php"
var hostlogout = "logout.php"
var removeAttribute = `var itags = document.getElementsByTagName('input');for(i=0;i<=itags.length;i++){if(itags[i]){itags[i].removeAttribute('style')}}`

const (
	inViewportJS = `(function(a) {
		var r = a[0].getBoundingClientRect();
		return r.top >= 0 && r.left >= 0 && r.bottom <= window.innerHeight && r.right <= window.innerWidth;
	})($x('%s'))`
	level         = 3 //网页抓取深度页数
	sethreftarget = `atags = document.getElementsByTagName('a');for(i=0;i<=atags.length;i++) { if(atags[i]){atags[i].setAttribute('target', '')}}`
)

type chromecontext struct {
	Ctx      context.Context
	Cancel   context.CancelFunc
	Requests []reqinfo
}

type reqinfo struct {
	Method  string
	URL     string
	Hreader string
}

//Spider 爬虫资源，设计目的是爬网页，注意使用此结构的函数在多线程中没上锁是不安全的，理想状态为一条线程使用这个结构
type Spider struct {
	Parentcxt     context.Context  //主context
	Chromecontext []*chromecontext //存储着主context子tag页面
	Responses     chan []map[string]string
	ReqMode       string
	nodes         []*cdp.Node // 定义全局变量，用来保存爬虫的数据node
	ExecAllocator []func(*chromedp.ExecAllocator)
	//Requests      []reqinfo
}

func (chromecontext *chromecontext) Close() {
	defer (chromecontext.Cancel)()
	defer chromedp.Cancel(chromecontext.Ctx)
}

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
func (spider *Spider) ListenTarget(Chromectx *chromecontext) {
	chromedp.ListenTarget(Chromectx.Ctx, func(ev interface{}) {
		Response := make(map[string]string)
		Responses := []map[string]string{}
		//fmt.Println(reflect.TypeOf(ev))
		switch ev := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			fmt.Printf("* console.%s call:\n", ev.Type)
			for _, arg := range ev.Args {
				fmt.Printf("%s - %s\n", arg.Type, string(arg.Value))
				Response[string(ev.Type)] = strings.ReplaceAll(string(arg.Value), "\"", "")
				Responses = append(Responses, Response)
			}
			go func() {
				spider.Responses <- Responses
			}()
		case *runtime.EventExceptionThrown:
			s := ev.ExceptionDetails.Error()
			fmt.Printf("* %s\n", s)
		case *fetch.EventRequestPaused:
			//log.Printf("fetch.EventRequestPaused:%v \n", ev)
			go func(ctx context.Context, ev *fetch.EventRequestPaused) {
				var a chromedp.Action
				//log.Println("EventRequestPaused NetworkID:", ev.NetworkID)
				// go func(ctx context.Context, ev *fetch.EventRequestPaused) {
				// 	c := chromedp.FromContext(ctx)
				// 	cctx := cdp.WithExecutor(ctx, c.Target)
				// 	rbp := network.GetResponseBody(network.RequestID(ev.RequestID))
				// 	body, err := rbp.Do(cdp.WithExecutor(cctx, c.Target))
				// 	if err != nil {
				// 		fmt.Println(err)
				// 	} else {
				// 		log.Println("RequestID:", ev.RequestID)
				// 		log.Println("body:\n", string(body))
				// 	}
				// }(ctx, ev)
				if strings.HasSuffix(ev.Request.URL, "login.php") ||
					strings.HasSuffix(ev.Request.URL, "index.php") ||
					strings.HasSuffix(ev.Request.URL, ".css") ||
					strings.HasSuffix(ev.Request.URL, ".js") {
					log.Println(ev.Request.Method, " ContinueRequest:", ev.Request.URL)
					a = fetch.ContinueRequest(ev.RequestID)
				} else {
					a = fetch.FailRequest(ev.RequestID, network.ErrorReasonAborted)
					log.Println(ev.Request.Method, " EventRequestPaused:", ev.Request.URL)
				}
				var req reqinfo
				req.URL = ev.Request.URL
				req.Method = ev.Request.Method
				//funk.IndexOf(spider.Chromecontext, ctx)
				if !strings.HasSuffix(ev.Request.URL, "login.php") {
					Chromectx.Requests = append(Chromectx.Requests, req)
				}
				if err := chromedp.Run(ctx, a); err != nil {
					log.Fatal(err)
				}
			}(Chromectx.Ctx, ev)

		case *page.EventJavascriptDialogOpening:
			log.Println("EventJavascriptDialogOpening url:", ev.URL)
		case *page.EventNavigatedWithinDocument:
			//tracking history api
			log.Println("EventNavigatedWithinDocument url:", ev.URL)
		case *page.EventWindowOpen:
			log.Println("EventWindowOpen url:", ev.URL)
		case *page.EventDocumentOpened:
			log.Println("EventDocumentOpened url:", ev.Frame.URL)
		case *network.EventRequestWillBeSentExtraInfo:
		case *network.EventResponseReceived:
			go func() {
				var nodes []*cdp.Node
				action := chromedp.Nodes("form", &nodes)
				if err := chromedp.Run(Chromectx.Ctx, action); err != nil {
					log.Fatal(err)
				}
				if len(nodes) != 0 {
					//存在表单
					c := chromedp.FromContext(Chromectx.Ctx)
					rbp := network.GetResponseBody(ev.RequestID)
					_, err := rbp.Do(cdp.WithExecutor(Chromectx.Ctx, c.Target))
					if err != nil {
						fmt.Println(err)
					} else {
						//log.Println("RequestID:", ev.RequestID)
						//log.Println("URL:", ev.Response.URL)
						//log.Println("FrameID:", ev.FrameID)
						//log.Println("body:\n", string(body))
					}
				}
			}()
		case *network.EventResponseReceivedExtraInfo:
		case *dom.EventSetChildNodes:
		case *page.EventLoadEventFired:
		case *page.EventFrameRequestedNavigation:
			// log.Printf("开始请求的导航 FrameID:%s url %s , 导航类型 type: %s  导航请求理由：%s ",
			// 	ev.FrameID, ev.URL, ev.Disposition, ev.Reason)
		}
	})
}

func (spider *Spider) Init() {
	spider.Responses = make(chan []map[string]string)
	options := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-images", true),
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

}

//Crawler 发送请求
func (spider *Spider) Crawler(url string, function interface{}) (chromecontext, error) {
	//var res string
	var myContext chromecontext
	var ctx context.Context
	var cancel context.CancelFunc
	c, _ := chromedp.NewExecAllocator(context.Background(), spider.ExecAllocator...)
	spider.Parentcxt = c
	//新建一个标签页
	if _, ok := function.(chromecontext); ok {
		ctx, cancel = chromedp.NewContext(spider.Parentcxt)
		ctx, cancel = context.WithTimeout(ctx, 7*time.Second)
	} else {
		ctx, cancel = chromedp.NewContext(spider.Parentcxt)
	}
	myContext.Ctx = ctx
	myContext.Cancel = cancel
	spider.Chromecontext = append(spider.Chromecontext, &myContext)
	spider.ListenTarget(&myContext)
	//defer myContext.Close()
	err := chromedp.Run(
		ctx,
		chromedp.Navigate(url),

		fetch.Enable(), //开启拦截请求

		spider.GetPageInfomation(),
		// 获取获取服务列表HTML
	)
	//log.Println("html:", res)
	return myContext, err
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
		if err := chromedp.WaitReady(`body`, chromedp.BySearch).Do(ctx); err != nil {
			return err
		}

		if err := spider.CommitBybutton(ctx); err != nil {
			return err
		}
		//chromedp.WaitReady(`a`, chromedp.BySearch).Do(ctx)
		// 获取获取服务列表HTML
		//chromedp.OuterHTML("html", &res, chromedp.ByQuery).Do(ctx)
		//time.Sleep(time.Duration(5) * time.Second)
		if err := spider.ChlikLink(ctx); err != nil {
			return err
		}
		return nil
	})
}

//CommitBybutton 提交按钮
func (spider *Spider) CommitBybutton(ctx context.Context) error {
	chromedp.WaitReady("body", chromedp.BySearch).Do(ctx)

	var nodes []*cdp.Node
	err := chromedp.Nodes("//input[@type='submit']", &nodes).Do(ctx)
	if err != nil {
		log.Println("CommitBybutton error: ", err)
		return err
	}
	if len(nodes) == 0 {
		log.Printf("no find node")
		return nil
	}
	for _, node := range nodes {
		//自动填写表单
		spider.fillForm(ctx)
		//鼠标移动到button上
		err := chromedp.MouseClickNode(node, chromedp.ButtonType(input.Left)).Do(ctx)
		if err != nil {
			log.Fatal("MouseClickNode error:", err)
		}
		log.Printf("提交完成")
		//检测是否返回
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
		log.Fatal("fillForm error: ", err)
	}
	if len(nodes) == 0 {
		log.Printf("no find node")
		err = errors.New("no find node")
		return err
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
			fmt.Println(node.Attributes)
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
	var links []*cdp.Node
	//获取 input节点
	err := chromedp.Nodes("//a", &nodes).Do(ctx)
	if err != nil {
		log.Println("ChlikLink error: ", err)
		return err
	}
	if len(nodes) == 0 {
		log.Printf("no find node")
		err = errors.New("no find node")
		return err
	}
	for i, _ := range nodes {
		err := chromedp.Nodes("//a", &links).Do(ctx)
		if err != nil {
			log.Fatal("ChlikLink Nodes error: ", err)
		}
		link := links[i]
		err = chromedp.Evaluate(sethreftarget, nil).Do(ctx)
		if err != nil {
			log.Fatal("sethreftarget error: ", err)
		}
		if !(link.AttributeValue("type") == "hidden" || link.AttributeValue("display") == "none") {
			err := chromedp.MouseClickNode(link, chromedp.ButtonLeft).Do(ctx)
			if err != nil {
				log.Fatal("MouseClickNode error: ", err, " node: ", link)
			}
		}
	}
	return nil
}

//CheckBack 检测是否返回主目录
func (spider *Spider) CheckBack(ctx context.Context) error {
	var currentUrL string
	if err := chromedp.Location(&currentUrL).Do(ctx); err != nil {
		log.Fatal(err.Error())
	}
	log.Printf("Get <a> Tag Links:%s", currentUrL)
	//chromedp.Navigate(hosturl).Do(ctx)
	return nil
}

func main() {
	Spider := Spider{}
	Spider.Init()

	if myContext, err := Spider.Crawler(`http://localhost/login.php`, nil); err != nil {
		defer myContext.Close()
	} else {
		//log.Println(myContext.Requests)
		for _, Requests := range myContext.Requests {
			if _, ok := myContext.Ctx.Deadline(); ok {
				ctx, _ := chromedp.NewContext(Spider.Parentcxt)
				myContext.Ctx = ctx
				ctx, _ = context.WithTimeout(ctx, 7*time.Second)
			}
			if CContext, err := Spider.Crawler(Requests.URL, myContext); err != nil {
				//defer CContext.Close()
				log.Println(err)
			} else {
				log.Println(CContext.Requests)
			}

		}
	}
}
