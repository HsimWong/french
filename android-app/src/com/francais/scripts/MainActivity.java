package com.francais.scripts;

import android.app.Activity;
import android.graphics.Color;
import android.os.Build;
import android.os.Bundle;
import android.view.KeyEvent;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

/**
 * 极薄的壳：只做一件事——打开移动版页面。
 * 内容每天更新在网页端，本 APK 不需要重新打包。
 */
public class MainActivity extends Activity {

    private static final String PAGE_URL = "https://47.94.236.198/french/#/speaking";

    private WebView web;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        web = new WebView(this);
        web.setBackgroundColor(Color.WHITE);
        web.setOverScrollMode(WebView.OVER_SCROLL_NEVER);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setCacheMode(WebSettings.LOAD_DEFAULT);
        s.setUseWideViewPort(true);
        s.setLoadWithOverviewMode(true);
        s.setSupportZoom(false);
        s.setBuiltInZoomControls(false);
        s.setTextZoom(100);
        // 部分前端会识别 WebView 标记（; wv）后拒绝渲染，去掉它更接近普通 Chrome
        s.setUserAgentString(s.getUserAgentString().replace("; wv", ""));
        if (Build.VERSION.SDK_INT >= 21) {
            s.setMixedContentMode(WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE);
        }

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, String url) {
                if (url != null && url.startsWith("retry:")) {
                    view.reload();
                    return true;
                }
                return false;
            }

            @Override
            public void onReceivedError(WebView view, int errorCode, String description,
                                        String failingUrl) {
                showError(description);
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request,
                                        android.webkit.WebResourceError error) {
                if (Build.VERSION.SDK_INT >= 23 && request.isForMainFrame()) {
                    showError(String.valueOf(error.getDescription()));
                }
            }
        });

        setContentView(web);
        web.loadUrl(cacheBust(PAGE_URL));
    }

    /**
     * 加时间戳避免 WebView 命中旧缓存。
     * 有 hash 路由时必须插在 # 之前，否则会污染 SPA 的路由。
     */
    private static String cacheBust(String url) {
        int hash = url.indexOf('#');
        String base = hash >= 0 ? url.substring(0, hash) : url;
        String frag = hash >= 0 ? url.substring(hash) : "";
        String sep = base.indexOf('?') >= 0 ? "&" : "?";
        return base + sep + "t=" + System.currentTimeMillis() + frag;
    }

    private void showError(String detail) {
        String html = "<!DOCTYPE html><html><head><meta charset='utf-8'>"
                + "<meta name='viewport' content='width=device-width,initial-scale=1'>"
                + "<style>body{margin:0;padding:56px 28px;font-family:-apple-system,'PingFang SC',sans-serif;"
                + "background:#fdfcfa;color:#17181a;line-height:1.7}"
                + "h2{font-size:17px;margin:0 0 10px}p{color:#8b8f97;font-size:14px;margin:0 0 26px}"
                + "a{display:inline-block;padding:12px 26px;border:1px solid #e8e9ec;border-radius:10px;"
                + "background:#fff;color:#17181a;text-decoration:none;font-size:14px}</style></head><body>"
                + "<h2>页面没打开</h2><p>网络可能不通，或者页面暂时不可用。<br>" + detail + "</p>"
                + "<a href='retry:go'>重试</a></body></html>";
        web.loadDataWithBaseURL(null, html, "text/html", "utf-8", null);
    }

    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        if (keyCode == KeyEvent.KEYCODE_BACK && web != null && web.canGoBack()) {
            web.goBack();
            return true;
        }
        return super.onKeyDown(keyCode, event);
    }
}
