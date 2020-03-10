package ginrpc

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/xxjwxc/public/errors"
	"github.com/xxjwxc/public/message"
	"github.com/xxjwxc/public/myast"
	"github.com/xxjwxc/public/mybigcamel"
	"github.com/xxjwxc/public/mydoc"
	"github.com/xxjwxc/public/myreflect"
)

func (b *_Base) parseReqResp(typ reflect.Type, isObj bool) (reflect.Type, reflect.Type) {
	return nil, nil
}

// checkHandlerFunc Judge whether to match rules
func (b *_Base) checkHandlerFunc(typ reflect.Type, isObj bool) (int, bool) { // 判断是否匹配规则,返回参数个数
	offset := 0
	if isObj {
		offset = 1
	}
	num := typ.NumIn() - offset
	if num == 1 || num == 2 { // Parameter checking 参数检查
		ctxType := typ.In(0 + offset)

		// go-gin default method
		if ctxType == reflect.TypeOf(&gin.Context{}) {
			return num, true
		}

		// Customized context . 自定义的context
		if ctxType == b.apiType {
			return num, true
		}
	}
	return num, false
}

// HandlerFunc Get and filter the parameters to be bound (object call type)
func (b *_Base) handlerFuncObj(tvl, obj reflect.Value) gin.HandlerFunc { // 获取并过滤要绑定的参数(obj 对象类型)
	typ := tvl.Type()
	if typ.NumIn() == 2 { // Parameter checking 参数检查
		ctxType := typ.In(1)

		// go-gin default method
		apiFun := func(c *gin.Context) interface{} { return c }
		if ctxType == b.apiType { // Customized context . 自定义的context
			apiFun = b.apiFun
		} else if !(ctxType == reflect.TypeOf(&gin.Context{})) {
			panic("method " + runtime.FuncForPC(tvl.Pointer()).Name() + " not support!")
		}

		return func(c *gin.Context) {
			tvl.Call([]reflect.Value{obj, reflect.ValueOf(apiFun(c))})
		}
	}

	// Custom context type with request parameters .自定义的context类型,带request 请求参数
	call, err := b.getCallFunc3(tvl, obj)
	if err != nil { // Direct reporting error.
		panic(err)
	}

	return call
}

// Custom context type with request parameters
func (b *_Base) getCallFunc3(tvls ...reflect.Value) (func(*gin.Context), error) {
	offset := 0
	if len(tvls) > 1 {
		offset = 1
	}

	tvl := tvls[0]
	typ := tvl.Type()
	if typ.NumIn() != (2 + offset) { // Parameter checking 参数检查
		return nil, errors.New("method " + runtime.FuncForPC(tvl.Pointer()).Name() + " not support!")
	}

	if typ.NumOut() != 0 {
		if typ.NumOut() == 2 { // Parameter checking 参数检查
			if returnType := typ.Out(1); returnType != typeOfError {
				return nil, errors.Errorf("method : %v , returns[1] %v not error",
					runtime.FuncForPC(tvl.Pointer()).Name(), returnType.String())
			}
		} else {
			return nil, errors.Errorf("method : %v , Only 2 return values (obj, error) are supported", runtime.FuncForPC(tvl.Pointer()).Name())
		}
	}

	ctxType, reqType := typ.In(0+offset), typ.In(1+offset)

	reqIsGinCtx := false
	if ctxType == reflect.TypeOf(&gin.Context{}) {
		reqIsGinCtx = true
	}

	// ctxType != reflect.TypeOf(gin.Context{}) &&
	// ctxType != reflect.Indirect(reflect.ValueOf(b.iAPIType)).Type()
	if !reqIsGinCtx && ctxType != b.apiType {
		return nil, errors.New("method " + runtime.FuncForPC(tvl.Pointer()).Name() + " first parm not support!")
	}

	reqIsValue := true
	if reqType.Kind() == reflect.Ptr {
		reqIsValue = false
	}
	apiFun := func(c *gin.Context) interface{} { return c }
	if !reqIsGinCtx {
		apiFun = b.apiFun
	}

	return func(c *gin.Context) {
		req := reflect.New(reqType)
		if reqIsValue {
			req = reflect.New(reqType)
		} else {
			req = reflect.New(reqType.Elem())
		}
		if err := b.unmarshal(c, req.Interface()); err != nil { // Return error message.返回错误信息
			var fields []string
			if _, ok := err.(validator.ValidationErrors); ok {
				for _, err := range err.(validator.ValidationErrors) {
					tmp := fmt.Sprintf("%v:%v", myreflect.FindTag(req.Interface(), err.Field(), "json"), err.Tag())
					if len(err.Param()) > 0 {
						tmp += fmt.Sprintf("[%v](but[%v])", err.Param(), err.Value())
					}
					fields = append(fields, tmp)
					// fmt.Println(err.Namespace())
					// fmt.Println(err.Field())
					// fmt.Println(err.StructNamespace()) // can differ when a custom TagNameFunc is registered or
					// fmt.Println(err.StructField())     // by passing alt name to ReportError like below
					// fmt.Println(err.Tag())
					// fmt.Println(err.ActualTag())
					// fmt.Println(err.Kind())
					// fmt.Println(err.Type())
					// fmt.Println(err.Value())
					// fmt.Println(err.Param())
					// fmt.Println()
				}
			} else if _, ok := err.(*json.UnmarshalTypeError); ok {
				err := err.(*json.UnmarshalTypeError)
				tmp := fmt.Sprintf("%v:%v(but[%v])", err.Field, err.Type.String(), err.Value)
				fields = append(fields, tmp)

			} else {
				fields = append(fields, err.Error())
			}

			msg := message.GetErrorMsg(message.ParameterInvalid)
			msg.Error = fmt.Sprintf("req param : %v", strings.Join(fields, ";"))
			c.JSON(http.StatusBadRequest, msg)
			return
		}

		if reqIsValue {
			req = req.Elem()
		}
		var returnValues []reflect.Value
		if offset > 0 {
			returnValues = tvl.Call([]reflect.Value{tvls[1], reflect.ValueOf(apiFun(c)), req})
		} else {
			returnValues = tvl.Call([]reflect.Value{reflect.ValueOf(apiFun(c)), req})
		}
		if returnValues != nil {
			obj := returnValues[0].Interface()
			rerr := returnValues[1].Interface()
			if rerr != nil {
				msg := message.GetErrorMsg(message.InValidOp)
				msg.Error = rerr.(error).Error()
				c.JSON(http.StatusBadRequest, msg)
			} else {
				c.JSON(http.StatusOK, obj)
			}
		}
	}, nil
}

func (b *_Base) unmarshal(c *gin.Context, v interface{}) error {
	return c.ShouldBind(v)
}

func (b *_Base) parserStruct(req, resp *parmInfo, astPkg *ast.Package, modPkg, modFile string) (r, p *mydoc.StructInfo) {
	ant := myast.NewStructAnalys(modPkg, modFile)
	if req != nil {
		tmp := astPkg
		if len(req.Pkg) > 0 {
			objFile := myast.EvalSymlinks(modPkg, modFile, req.Import)
			tmp, _ = myast.GetAstPkgs(req.Pkg, objFile) // get ast trees.
		}
		r = ant.ParserStruct(tmp, req.Type)
	}

	if resp != nil {
		tmp := astPkg
		if len(resp.Pkg) > 0 {
			objFile := myast.EvalSymlinks(modPkg, modFile, resp.Import)
			tmp, _ = myast.GetAstPkgs(resp.Pkg, objFile) // get ast trees.
		}
		p = ant.ParserStruct(tmp, resp.Type)
	}

	return
}

var routeRegex = regexp.MustCompile(`@Router\s+(\S+)(?:\s+\[(\S+)\])?`)

func (b *_Base) parserComments(f *ast.FuncDecl, objName, objFunc string, imports map[string]string, objPkg string, num int) ([]genComment, *parmInfo, *parmInfo) {
	var note string
	var gcs []genComment
	var req, resp *parmInfo
	if f.Type.Params != nil {
		if f.Type.Params.NumFields() > 1 {
			req = &parmInfo{}
			d := f.Type.Params.List[1].Type
			switch exp := d.(type) {
			case *ast.SelectorExpr: // 非本文件包
				req.Type = exp.Sel.Name
				if x, ok := exp.X.(*ast.Ident); ok {
					req.Import = imports[x.Name]
					req.Pkg = myast.GetImportPkg(req.Import)
				}
			case *ast.StarExpr: // 本文件
				if x, ok := exp.X.(*ast.Ident); ok {
					req.Type = x.Name
					req.Import = objPkg // 本包
				}
			}
		}
	}
	if f.Type.Results != nil {
		if f.Type.Results.NumFields() > 1 {
			resp = &parmInfo{}
			d := f.Type.Results.List[0].Type
			if exp, ok := d.(*ast.StarExpr); ok {
				switch expx := exp.X.(type) {
				case *ast.SelectorExpr: // 非本地包
					resp.Type = expx.Sel.Name
					if x, ok := expx.X.(*ast.Ident); ok {
						resp.Pkg = x.Name
						resp.Import = imports[resp.Pkg]
					}
				case *ast.Ident: // 本文件
					resp.Type = expx.Name
					resp.Import = objPkg // 本包
				}
			}
		}
	}

	if f.Doc != nil {
		for _, c := range f.Doc.List {
			gc := genComment{}
			t := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if strings.HasPrefix(t, "@Router") {
				// t := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				matches := routeRegex.FindStringSubmatch(t)
				if len(matches) == 3 {
					gc.RouterPath = matches[1]
					methods := matches[2]
					if methods == "" {
						gc.Methods = []string{"get"}
					} else {
						gc.Methods = strings.Split(methods, ",")
					}
					gcs = append(gcs, gc)
				} else {
					// return nil, errors.New("Router information is missing")
				}
			} else if strings.HasPrefix(t, objFunc) { // find note
				t = strings.TrimSpace(strings.TrimPrefix(t, objFunc))
				note += t
			}
		}

	}

	//defalt
	if len(gcs) == 0 {
		gc := genComment{}
		gc.RouterPath, gc.Methods = b.getDefaultComments(objName, objFunc, num)
		gcs = append(gcs, gc)
	}

	// add note 添加注释
	for i := 0; i < len(gcs); i++ {
		gcs[i].Note = note
	}

	return gcs, req, resp
}

// tryGenRegister gen out the Registered config info  by struct object,[prepath + bojname.]
func (b *_Base) tryGenRegister(router *gin.Engine, cList ...interface{}) bool {
	modPkg, modFile, isFind := myast.GetModuleInfo(2)
	if !isFind {
		return false
	}

	doc := mydoc.NewDoc(b.groupPath)

	for _, c := range cList {
		refVal := reflect.ValueOf(c)
		t := reflect.Indirect(refVal).Type()
		objPkg := t.PkgPath()
		objName := t.Name()
		// fmt.Println(objPkg, objName)

		// find path
		objFile := myast.EvalSymlinks(modPkg, modFile, objPkg)
		// fmt.Println(objFile)

		astPkgs, _b := myast.GetAstPkgs(objPkg, objFile) // get ast trees.
		if _b {
			imports := myast.AnalysisImport(astPkgs)
			funMp := myast.GetObjFunMp(astPkgs, objName)
			// ast.Print(token.NewFileSet(), astPkgs)
			// fmt.Println(b)

			refTyp := reflect.TypeOf(c)
			// Install the methods
			for m := 0; m < refTyp.NumMethod(); m++ {
				method := refTyp.Method(m)
				num, _b := b.checkHandlerFunc(method.Type /*.Interface()*/, true)
				if _b {
					if sdl, ok := funMp[method.Name]; ok {
						gcs, req, resp := b.parserComments(sdl, objName, method.Name, imports, objPkg, num)
						docReq, docResp := b.parserStruct(req, resp, astPkgs, modPkg, modFile)
						fmt.Println(method.Name, docReq, docResp)
						for _, gc := range gcs {
							doc.AddOne(objName, gc.RouterPath, gc.Methods, docReq, docResp)
							checkOnceAdd(objName+"."+method.Name, gc.RouterPath, gc.Methods)
						}
					}
				}
			}
		}
	}

	doc.GenSwagger(modFile + "/docs/swagger/")
	doc.GenMarkdown(modFile + "/docs/markdown/")
	genOutPut(b.outPath, modFile) // generate code
	return true
}

// register Registered by struct object,[prepath + bojname.]
func (b *_Base) register(router *gin.Engine, cList ...interface{}) bool {
	mp := getInfo()
	for _, c := range cList {
		refTyp := reflect.TypeOf(c)
		refVal := reflect.ValueOf(c)
		t := reflect.Indirect(refVal).Type()
		objName := t.Name()

		// Install the methods
		for m := 0; m < refTyp.NumMethod(); m++ {
			method := refTyp.Method(m)
			num, _b := b.checkHandlerFunc(method.Type /*.Interface()*/, true)
			if _b {
				if v, ok := mp[objName+"."+method.Name]; ok {
					for _, v1 := range v {
						b.registerHandlerObj(router, v1.Methods, buildRelativePath(b.groupPath, v1.RouterPath), method.Func, refVal)
					}
				} else { // not find using default case
					routerPath, methods := b.getDefaultComments(objName, method.Name, num)
					b.registerHandlerObj(router, methods, buildRelativePath(b.groupPath, routerPath), method.Func, refVal)
				}
			}
		}
	}
	return true
}

func (b *_Base) getDefaultComments(objName, objFunc string, num int) (routerPath string, methods []string) {
	methods = []string{"get"}
	if num == 2 { // parm 2 , post default
		methods = []string{"post"}
	}

	if b.isBigCamel { // big camel style.大驼峰
		routerPath = objName + "." + objFunc
	} else {
		routerPath = mybigcamel.UnMarshal(objName) + "." + mybigcamel.UnMarshal(objFunc)
	}

	return
}

// registerHandlerObj Multiple registration methods.获取并过滤要绑定的参数
func (b *_Base) registerHandlerObj(router *gin.Engine, httpMethod []string, relativePath string, tvl, obj reflect.Value) error {
	call := b.handlerFuncObj(tvl, obj)

	for _, v := range httpMethod {
		// method := strings.ToUpper(v)
		// switch method{
		// case "ANY":
		// 	router.Any(relativePath,list...)
		// default:
		// 	router.Handle(method,relativePath,list...)
		// }
		// or
		switch strings.ToUpper(v) {
		case "POST":
			router.POST(relativePath, call)
		case "GET":
			router.GET(relativePath, call)
		case "DELETE":
			router.DELETE(relativePath, call)
		case "PATCH":
			router.PATCH(relativePath, call)
		case "PUT":
			router.PUT(relativePath, call)
		case "OPTIONS":
			router.OPTIONS(relativePath, call)
		case "HEAD":
			router.HEAD(relativePath, call)
		case "ANY":
			router.Any(relativePath, call)
		default:
			return errors.Errorf("method:[%v] not support", httpMethod)
		}
	}

	return nil
}
