package com.wails.app;

import android.animation.ValueAnimator;
import android.content.Context;
import android.content.res.Configuration;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.LinearGradient;
import android.graphics.Matrix;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.Shader;
import android.graphics.Typeface;
import android.util.AttributeSet;
import android.view.View;
import android.view.animation.LinearInterpolator;

/** Draw the existing loading wordmark and refresh spinner without waiting for WebView or Go. */
public final class StartupView extends View {
    private final Paint wordmark = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint spinner = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Path arrows = new Path();
    private final Matrix gradientOffset = new Matrix();
    private final ValueAnimator animation = ValueAnimator.ofFloat(0f, 1f);
    private float phase;

    public StartupView(Context context, AttributeSet attributes) {
        super(context, attributes);
        setBackgroundColor(Color.rgb(10, 10, 10));
        setClickable(true);
        setContentDescription("Yousage");
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
        wordmark.setTypeface(Typeface.create("sans-serif", Typeface.BOLD));
        wordmark.setTextAlign(Paint.Align.CENTER);
        spinner.setStyle(Paint.Style.STROKE);
        spinner.setStrokeWidth(2f);
        spinner.setStrokeCap(Paint.Cap.ROUND);
        spinner.setStrokeJoin(Paint.Join.ROUND);
        arrows.moveTo(3, 12);
        arrows.cubicTo(3, 7, 7, 3, 12, 3);
        arrows.cubicTo(15, 3, 17, 4, 21, 8);
        arrows.moveTo(21, 3); arrows.lineTo(21, 8); arrows.lineTo(16, 8);
        arrows.moveTo(21, 12);
        arrows.cubicTo(21, 17, 17, 21, 12, 21);
        arrows.cubicTo(9, 21, 7, 20, 3, 16);
        arrows.moveTo(8, 16); arrows.lineTo(3, 16); arrows.lineTo(3, 21);
        animation.setDuration(6000);
        animation.setRepeatCount(ValueAnimator.INFINITE);
        animation.setInterpolator(new LinearInterpolator());
        animation.addUpdateListener(value -> {
            phase = (float) value.getAnimatedValue();
            invalidate();
        });
    }

    @Override
    protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        float scale = getResources().getDisplayMetrics().density;
        float textSize = 28f * getResources().getDisplayMetrics().scaledDensity;
        wordmark.setTextSize(textSize);
        boolean isDark = (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
                == Configuration.UI_MODE_NIGHT_YES;
        int start = Color.parseColor(isDark ? "#a99bff" : "#6d5ce8");
        int end = Color.parseColor(isDark ? "#c6ec6e" : "#5f8f14");
        float width = wordmark.measureText("Yousage");
        float left = (getWidth() - width) / 2f;
        LinearGradient gradient = new LinearGradient(left, 0, left + width * 2, 0,
                new int[]{start, end, start, end, start}, null, Shader.TileMode.REPEAT);
        gradientOffset.setTranslate(-phase * width, 0);
        gradient.setLocalMatrix(gradientOffset);
        wordmark.setShader(gradient);
        float top = (getHeight() - textSize - 36f * scale) / 2f;
        Paint.FontMetrics metrics = wordmark.getFontMetrics();
        canvas.drawText("Yousage", getWidth() / 2f,
                top + (textSize - metrics.descent - metrics.ascent) / 2f, wordmark);
        spinner.setColor(Color.parseColor(isDark ? "#a3a3a3" : "#737373"));
        canvas.save();
        canvas.translate(getWidth() / 2f, top + textSize + 28f * scale);
        canvas.rotate(phase * 2160f);
        canvas.scale(16f * scale / 24f, 16f * scale / 24f);
        canvas.translate(-12f, -12f);
        canvas.drawPath(arrows, spinner);
        canvas.restore();
    }

    /** Stop consuming frames when hidden, backgrounded or detached. */
    private void updateAnimation() {
        if (isAttachedToWindow() && isShown() && getWindowVisibility() == VISIBLE) {
            if (!animation.isStarted()) animation.start();
        } else {
            animation.cancel();
        }
    }

    @Override
    protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        updateAnimation();
    }

    @Override
    protected void onVisibilityChanged(View changedView, int visibility) {
        super.onVisibilityChanged(changedView, visibility);
        if (animation != null) updateAnimation();
    }

    @Override
    protected void onWindowVisibilityChanged(int visibility) {
        super.onWindowVisibilityChanged(visibility);
        if (animation != null) updateAnimation();
    }

    @Override
    protected void onDetachedFromWindow() {
        animation.cancel();
        super.onDetachedFromWindow();
    }
}
